// Package auth covers signature and identity failures and multi-identity
// authorization: matrix IDs A01-A05 in docs/test-matrix.md.
package auth

import (
	"bytes"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/upayanmazumder-DevLabs/seaweedfs-s3-harness/internal/s3x"
	"github.com/upayanmazumder-DevLabs/seaweedfs-s3-harness/internal/testkit"
)

// A01: an anonymous (unsigned) request against an auth-enabled target must
// be rejected.
func TestAnonymousRejected(t *testing.T) {
	tg := testkit.Target(t)
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "auth")

	anon := s3x.NewAnonymous(tg)
	_, err := anon.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket)})
	if err == nil {
		t.Fatal("anonymous ListObjectsV2 succeeded against an auth-enabled target")
	}
	if status := testkit.StatusCode(err); status != 403 {
		t.Errorf("status: want 403 got %d (code %s)", status, testkit.ErrorCode(err))
	}
}

// A02: a request signed with a wrong secret key must fail with
// SignatureDoesNotMatch.
func TestWrongSignature(t *testing.T) {
	tg := testkit.Target(t)
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "auth")

	bad := s3x.NewWithBadSignature(tg)
	_, err := bad.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket)})
	if err == nil {
		t.Fatal("ListObjectsV2 with a wrong signature succeeded")
	}
	if code := testkit.ErrorCode(err); code != "SignatureDoesNotMatch" {
		t.Errorf("error code: want SignatureDoesNotMatch got %q", code)
	}
}

// A04: a read-only identity can GET and LIST, but PUT and DELETE must be
// rejected.
func TestReadOnlyIdentityCannotWrite(t *testing.T) {
	tg := testkit.Target(t)
	testkit.SkipUnlessMultiIdentity(t, tg)
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "auth")
	key := testkit.Prefix(t) + "ro-test"

	if _, err := cl.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader([]byte("x"))}); err != nil {
		t.Fatalf("admin seed PutObject: %v", err)
	}

	ro := s3x.NewReadOnly(tg)
	if out, err := ro.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)}); err != nil {
		t.Fatalf("read-only GetObject should succeed: %v", err)
	} else {
		out.Body.Close()
	}
	if _, err := ro.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket)}); err != nil {
		t.Fatalf("read-only ListObjectsV2 should succeed: %v", err)
	}

	_, err := ro.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader([]byte("y"))})
	if err == nil {
		t.Fatal("read-only PutObject succeeded")
	}
	if status := testkit.StatusCode(err); status != 403 {
		t.Errorf("PutObject status: want 403 got %d", status)
	}

	_, err = ro.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err == nil {
		t.Fatal("read-only DeleteObject succeeded")
	}
	if status := testkit.StatusCode(err); status != 403 {
		t.Errorf("DeleteObject status: want 403 got %d", status)
	}
}

// A05: a scoped identity limited to one bucket must be rejected when
// touching another bucket.
func TestScopedIdentityCannotTouchOtherBucket(t *testing.T) {
	tg := testkit.Target(t)
	testkit.SkipUnlessMultiIdentity(t, tg)
	if tg.ScopedBucket == "" {
		t.Skip("target has no SWFSIT_SCOPED_BUCKET configured")
	}
	cl, ctx := testkit.Client(t, tg)
	otherBucket := testkit.Bucket(t, cl, ctx, "auth")

	scoped := s3x.NewScoped(tg)
	_, err := scoped.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(otherBucket)})
	if err == nil {
		t.Fatal("scoped identity listed a bucket outside its grant")
	}
	if status := testkit.StatusCode(err); status != 403 {
		t.Errorf("status: want 403 got %d", status)
	}
}
