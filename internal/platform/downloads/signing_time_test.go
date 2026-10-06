package downloads

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

func TestPresignUsesExpiryCalculationInstantAcrossSecondBoundary(t *testing.T) {
	issued := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	r, _ := http.NewRequest("GET", "https://objects.example.test/bucket/object?X-Amz-Expires=59", nil)
	s := fixedTimePresigner{v4.NewSigner(), issued}
	raw, _, err := s.PresignHTTP(t.Context(), aws.Credentials{AccessKeyID: "test", SecretAccessKey: "test-secret"}, r, "UNSIGNED-PAYLOAD", "s3", "us-east-1", issued.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(raw)
	if u.Query().Get("X-Amz-Date") != issued.Format("20060102T150405Z") || u.Query().Get("X-Amz-Expires") != "59" {
		t.Fatal("signature outlived the deadline calculation")
	}
}
