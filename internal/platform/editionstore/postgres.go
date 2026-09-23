// Package editionstore reads immutable SafePatch edition configuration.
package editionstore

import (
	"context"
	"database/sql"
	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/feature"
)

func Load(ctx context.Context, tx *sql.Tx, id string, features *feature.FeatureCatalog) (edition.Bundle, error) {
	var v edition.EditionVersion
	var sealed bool
	err := tx.QueryRowContext(ctx, `SELECT edition_version_id,edition_key,created_at,sealed FROM edition_versions WHERE edition_version_id=$1`, id).Scan(&v.EditionVersionID, &v.EditionKey, &v.CreatedAt, &sealed)
	if err != nil {
		return edition.Bundle{}, err
	}
	if !sealed || v.EditionVersionID != id {
		return edition.Bundle{}, edition.ErrInvalidEdition
	}
	rows, err := tx.QueryContext(ctx, `SELECT feature_id,enabled,limit_value,configuration_json::text FROM edition_features WHERE edition_version_id=$1 ORDER BY feature_id COLLATE "C" LIMIT 4097`, id)
	if err != nil {
		return edition.Bundle{}, err
	}
	mappings := []edition.EditionFeature{}
	total := 0
	for rows.Next() {
		m := edition.EditionFeature{EditionVersionID: id}
		var limit sql.NullInt64
		var raw string
		if err = rows.Scan(&m.FeatureID, &m.Enabled, &limit, &raw); err != nil {
			rows.Close()
			return edition.Bundle{}, err
		}
		total += len(raw)
		if len(mappings) >= feature.MaxFeatures || total > edition.MaxBundleConfigurationBytes {
			rows.Close()
			return edition.Bundle{}, edition.ErrInvalidEdition
		}
		if limit.Valid {
			value := limit.Int64
			m.LimitValue = &value
		}
		m.ConfigurationJSON = []byte(raw)
		mappings = append(mappings, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return edition.Bundle{}, err
	}
	if len(mappings) == 0 {
		return edition.Bundle{}, edition.ErrInvalidEdition
	}
	c, err := edition.NewCatalog(features)
	if err != nil {
		return edition.Bundle{}, err
	}
	c, err = c.Publish(v, mappings)
	if err != nil {
		return edition.Bundle{}, err
	}
	return c.GetVersion(id)
}
