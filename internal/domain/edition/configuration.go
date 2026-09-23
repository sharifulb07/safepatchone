package edition

import (
	"encoding/json"
	"safepatch/internal/domain/jsonobject"
)

// Preserve the edition error contract while sharing the existing validation.
func normalizeConfiguration(raw json.RawMessage) (json.RawMessage, error) {
	value, err := jsonobject.Normalize(raw, MaxConfigurationBytes)
	if err != nil {
		return nil, ErrInvalidEdition
	}
	return value, nil
}
