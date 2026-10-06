// Package downloads supplies HTTP and private object-store release distribution.
package downloads

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
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
	// Use one signing instant for both expiry arithmetic and the signature.
	// Letting SDK middleware sample a later second can extend the URL beyond
	// the caller's deadline and intermittently fail the application guard.
	issuedAt := time.Now().UTC().Truncate(time.Second)
	if a.AvailableUntil.Before(deadline) {
		deadline = a.AvailableUntil
	}
	ttl := deadline.Sub(issuedAt).Truncate(time.Second)
	if credentials.CanExpire {
		remaining := credentials.Expires.Sub(issuedAt).Truncate(time.Second)
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
	result, err := s3.NewPresignClient(client, func(o *s3.PresignOptions) {
		o.Presigner = fixedTimePresigner{v4.NewSigner(), issuedAt}
	}).PresignGetObject(ctx, input, func(o *s3.PresignOptions) { o.Expires = ttl })
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
	expires := issued.Add(time.Duration(seconds) * time.Second)
	if err != nil || e != nil || seconds < 1 || time.Duration(seconds)*time.Second > ttl || !expires.After(time.Now()) || expires.After(deadline) || (credentials.CanExpire && expires.After(credentials.Expires)) || ctx.Err() != nil {
		return application.DownloadLink{}, application.ErrDownloadDenied
	}
	return application.DownloadLink{URL: result.URL, ExpiresAt: expires}, nil
}

type fixedTimePresigner struct {
	*v4.Signer
	issuedAt time.Time
}

func (s fixedTimePresigner) PresignHTTP(ctx context.Context, credentials aws.Credentials, r *http.Request, payloadHash, service, region string, _ time.Time, options ...func(*v4.SignerOptions)) (string, http.Header, error) {
	return s.Signer.PresignHTTP(ctx, credentials, r, payloadHash, service, region, s.issuedAt, options...)
}
