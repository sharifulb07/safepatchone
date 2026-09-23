package organizationstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	"safepatch/internal/domain/jsonobject"
	org "safepatch/internal/domain/organization"
	"safepatch/internal/entitlement"
)

var _ entitlement.EntitlementRepository = (*Postgres)(nil)

func (s *Postgres) Create(ctx context.Context, organizationID string, record domain.Entitlement) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := entitlement.ValidateCreate(organizationID, record); err != nil {
		return err
	}
	v, err := domain.NewEntitlement(record, s.catalog)
	if err != nil {
		return err
	}
	if v.Source == domain.SourceSubscription && !org.ValidID(v.SourceReference) {
		return domain.ErrInvalidEntitlement
	}
	raw, err := json.Marshal(v)
	if err != nil || len(raw) > 32768 {
		return domain.ErrInvalidEntitlement
	}
	err = s.exec(ctx, `INSERT INTO entitlements (organization_id,entitlement_id,feature_id,source,source_reference,canonical_record) VALUES ($1,$2,$3,$4,$5,$6)`, organizationID, v.EntitlementID, v.FeatureID, string(v.Source), v.SourceReference, string(raw))
	if errors.Is(err, org.ErrConflict) {
		return entitlement.ErrAlreadyExists
	}
	return err
}
func (s *Postgres) decode(raw string, organizationID, id, featureID, source, reference string) (domain.Entitlement, error) {
	return DecodeEntitlement(s.catalog, raw, organizationID, id, featureID, source, reference)
}

// DecodeEntitlement shares strict canonical/projection validation with consistent
// transactional entitlement readers. Indexed fields must match the JSON record.
func DecodeEntitlement(catalog *feature.FeatureCatalog, raw string, organizationID, id, featureID, source, reference string) (domain.Entitlement, error) {
	checked, err := jsonobject.Normalize([]byte(raw), 32768)
	if err != nil {
		return domain.Entitlement{}, org.ErrData
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(checked, &fields) != nil || len(fields) != 11 {
		return domain.Entitlement{}, org.ErrData
	}
	for _, key := range []string{"entitlement_id", "organization_id", "feature_id", "source", "source_reference", "enabled", "limit_value", "starts_at", "expires_at", "metadata", "created_at"} {
		if _, ok := fields[key]; !ok {
			return domain.Entitlement{}, org.ErrData
		}
		if key != "limit_value" && key != "expires_at" && bytes.Equal(fields[key], []byte("null")) {
			return domain.Entitlement{}, org.ErrData
		}
	}
	var v domain.Entitlement
	if json.Unmarshal(checked, &v) != nil {
		return domain.Entitlement{}, org.ErrData
	}
	v, err = domain.NewEntitlement(v, catalog)
	if err != nil || v.OrganizationID != organizationID || v.EntitlementID != id || v.FeatureID != featureID || string(v.Source) != source || v.SourceReference != reference {
		return domain.Entitlement{}, org.ErrData
	}
	return v, nil
}
func (s *Postgres) Get(ctx context.Context, organizationID, id string) (domain.Entitlement, error) {
	if err := s.check(ctx); err != nil {
		return domain.Entitlement{}, err
	}
	if err := entitlement.ValidateGet(organizationID, id); err != nil {
		return domain.Entitlement{}, err
	}
	var raw, featureID, source, reference string
	err := s.db.QueryRowContext(ctx, `SELECT canonical_record,feature_id,source,source_reference FROM entitlements WHERE organization_id=$1 AND entitlement_id=$2`, organizationID, id).Scan(&raw, &featureID, &source, &reference)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Entitlement{}, entitlement.ErrNotFound
	}
	if err != nil {
		return domain.Entitlement{}, err
	}
	return s.decode(raw, organizationID, id, featureID, source, reference)
}
func (s *Postgres) List(ctx context.Context, organizationID string, q entitlement.ListQuery) (entitlement.Page, error) {
	failure := entitlement.Page{Entitlements: []domain.Entitlement{}}
	if err := s.check(ctx); err != nil {
		return failure, err
	}
	if err := entitlement.ValidateList(organizationID, q); err != nil {
		return failure, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT entitlement_id,canonical_record,feature_id,source,source_reference FROM entitlements
WHERE organization_id=$1 AND ($2='' OR feature_id=$2) AND entitlement_id COLLATE "C" > $3 COLLATE "C"
ORDER BY entitlement_id COLLATE "C" LIMIT $4`, organizationID, q.FeatureID, q.AfterID, q.Limit+1)
	if err != nil {
		return failure, err
	}
	defer rows.Close()
	page := entitlement.Page{Entitlements: []domain.Entitlement{}}
	previous := q.AfterID
	for rows.Next() {
		var id, raw, featureID, source, reference string
		if err := rows.Scan(&id, &raw, &featureID, &source, &reference); err != nil {
			return failure, err
		}
		v, err := s.decode(raw, organizationID, id, featureID, source, reference)
		if err != nil {
			return failure, err
		}
		if id <= previous || (q.FeatureID != "" && q.FeatureID != v.FeatureID) || len(page.Entitlements) > q.Limit {
			return failure, org.ErrData
		}
		previous = id
		page.Entitlements = append(page.Entitlements, v)
	}
	if err := rows.Err(); err != nil {
		return failure, err
	}
	if len(page.Entitlements) > q.Limit {
		page.Entitlements = page.Entitlements[:q.Limit]
		page.NextAfterID = page.Entitlements[q.Limit-1].EntitlementID
	}
	return page, nil
}
