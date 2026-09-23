package downloads

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"safepatch/internal/application"
	"safepatch/internal/domain/jsonobject"
	"safepatch/internal/domain/release"
)

type Configuration struct {
	Schema    string             `json:"schema"`
	Region    string             `json:"region"`
	Endpoint  string             `json:"endpoint"`
	Bucket    string             `json:"bucket"`
	Artifacts []release.Artifact `json:"artifacts"`
}

func DecodeConfiguration(raw []byte) (Configuration, error) {
	normalized, err := jsonobject.Normalize(raw, 2*1024*1024)
	if err != nil {
		return Configuration{}, ErrConfiguration
	}
	var config Configuration
	d := json.NewDecoder(bytes.NewReader(normalized))
	d.DisallowUnknownFields()
	if d.Decode(&config) != nil || config.Schema != "safepatch.downloads.v1" || len(config.Artifacts) == 0 || len(config.Artifacts) > 4096 {
		return Configuration{}, ErrConfiguration
	}
	// encoding/json accepts case aliases; configuration identities do not.
	var fields map[string]json.RawMessage
	if json.Unmarshal(normalized, &fields) != nil {
		return Configuration{}, ErrConfiguration
	}
	for key := range fields {
		switch key {
		case "schema", "region", "endpoint", "bucket", "artifacts":
		default:
			return Configuration{}, ErrConfiguration
		}
	}
	var entries []map[string]json.RawMessage
	if json.Unmarshal(fields["artifacts"], &entries) != nil {
		return Configuration{}, ErrConfiguration
	}
	for i, entry := range entries {
		for key := range entry {
			switch key {
			case "id", "version", "channel", "kind", "filename", "sha256", "bytes", "available_until", "organization_id", "object_version":
			default:
				return Configuration{}, ErrConfiguration
			}
		}
		v, e := config.Artifacts[i].Normalize()
		if e != nil {
			return Configuration{}, ErrConfiguration
		}
		config.Artifacts[i] = v
	}
	return config, nil
}

func NewConfigured(ctx context.Context, raw []byte, access application.PlatformAccess, auth application.Authenticator) (*Handler, error) {
	c, err := DecodeConfiguration(raw)
	if err != nil {
		return nil, err
	}
	config, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(c.Region))
	if err != nil {
		return nil, ErrConfiguration
	}
	signer, err := NewS3Signer(config, c.Endpoint, c.Bucket)
	if err != nil {
		return nil, err
	}
	service, err := application.NewDownloads(access, c.Artifacts, signer, time.Now)
	if err != nil {
		return nil, err
	}
	return NewHandler(service, auth)
}

// Downloads do not spend proof credits. HTTP concurrency and edge rate limits
// bound URL issuance; the existing authorization checks remain mandatory.
type Unmetered struct{}

func (Unmetered) Check(ctx context.Context, _ application.Execution) error { return ctx.Err() }
