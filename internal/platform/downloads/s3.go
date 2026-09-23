// Package downloads supplies HTTP and private object-store release distribution.
package downloads

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"safepatch/internal/application"
	"safepatch/internal/domain/release"
)

var ErrConfiguration = errors.New("invalid downloads configuration")
var bucketName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)

type S3Signer struct {
	config           aws.Config
	endpoint, bucket string
}

// The endpoint is trusted operator configuration, never a client URL. Presigning
// does not fetch it; SDK credentials use the configured standard provider chain.
func NewS3Signer(config aws.Config, endpoint, bucket string) (*S3Signer, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || !bucketName.MatchString(bucket) || config.Credentials == nil || config.Region == "" {
		return nil, ErrConfiguration
	}
	return &S3Signer{config, endpoint, bucket}, nil
}
func (s *S3Signer) Sign(ctx context.Context, a release.Artifact, deadline time.Time) (application.DownloadLink, error) {
	if s == nil || ctx == nil || ctx.Err() != nil || !deadline.After(time.Now()) || time.Until(deadline) > application.DownloadTTL {
		return application.DownloadLink{}, application.ErrDownloadDenied
	}
	canonical, err := a.Normalize()
	if err != nil || canonical != a {
		return application.DownloadLink{}, application.ErrDownloadDenied
	}
	credentials, err := s.config.Credentials.Retrieve(ctx)
	if err != nil || credentials.AccessKeyID == "" || credentials.SecretAccessKey == "" {
		return application.DownloadLink{}, application.ErrDownloadDenied
	}
	ttl := time.Until(deadline).Truncate(time.Second)
	if credentials.CanExpire {
		remaining := time.Until(credentials.Expires).Truncate(time.Second)
		if remaining < ttl {
			ttl = remaining
		}
		if ttl < time.Second {
			return application.DownloadLink{}, application.ErrDownloadDenied
		}
	}
	if ttl < time.Second {
		return application.DownloadLink{}, application.ErrDownloadDenied
	}
	config := s.config
	// Bind this signature to the credential snapshot whose expiry was checked.
	config.Credentials = aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) { return credentials, nil })
	client := s3.NewFromConfig(config, func(o *s3.Options) { o.BaseEndpoint = aws.String(s.endpoint); o.UsePathStyle = true })
	input := &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(a.Key()), ResponseContentDisposition: aws.String(`attachment; filename="` + a.Filename + `"`), ResponseContentType: aws.String("application/octet-stream"), ResponseCacheControl: aws.String("private, no-store")}
	if a.ObjectVersion != "" {
		input.VersionId = aws.String(a.ObjectVersion)
	}
	result, err := s3.NewPresignClient(client).PresignGetObject(ctx, input, func(o *s3.PresignOptions) { o.Expires = ttl })
	if err != nil {
		return application.DownloadLink{}, application.ErrDownloadDenied
	}
	u, err := url.Parse(result.URL)
	endpoint, _ := url.Parse(s.endpoint)
	if err != nil || result.Method != "GET" || u.Scheme != "https" || u.Host != endpoint.Host || u.Path != "/"+s.bucket+"/"+a.Key() || u.Query().Get("X-Amz-Signature") == "" {
		return application.DownloadLink{}, application.ErrDownloadDenied
	}
	issued, err := time.Parse("20060102T150405Z", u.Query().Get("X-Amz-Date"))
	seconds, e := strconv.Atoi(u.Query().Get("X-Amz-Expires"))
	if err != nil || e != nil || seconds < 1 || time.Duration(seconds)*time.Second > ttl {
		return application.DownloadLink{}, application.ErrDownloadDenied
	}
	return application.DownloadLink{URL: result.URL, ExpiresAt: issued.Add(time.Duration(seconds) * time.Second)}, nil
}
