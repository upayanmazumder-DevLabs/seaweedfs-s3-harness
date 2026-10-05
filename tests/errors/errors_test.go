// Package errors verifies S3 error responses are well-formed and carry the
// AWS-documented error codes and HTTP status, not just "some 4xx/5xx":
// matrix IDs E01-E02 in docs/test-matrix.md.
package errors

import (
	"bytes"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/upayanmazumder-DevLabs/seaweedfs-s3-harness/internal/testkit"
)

// E01/E02: a table of operations expected to fail, checked against both the
// S3 error Code and the HTTP status AWS documents for it.
func TestErrorCodesAndStatus(t *testing.T) {
	tg := testkit.Target(t)
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "errors")

	cases := []struct {
		name       string
		run        func() error
		wantCode   string
		wantStatus int
	}{
		{
			name: "GetObject/NoSuchKey",
			run: func() error {
				_, err := cl.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String("never-existed")})
				return err
			},
			wantCode: "NoSuchKey", wantStatus: 404,
		},
		{
			name: "GetObject/NoSuchBucket",
			run: func() error {
				_, err := cl.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("it-does-not-exist-" + testkit.RunID()), Key: aws.String("x")})
				return err
			},
			wantCode: "NoSuchBucket", wantStatus: 404,
		},
		{
			name: "CompleteMultipartUpload/NoSuchUpload",
			run: func() error {
				_, err := cl.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
					Bucket: aws.String(bucket), Key: aws.String("x"), UploadId: aws.String("bogus-upload-id"),
					MultipartUpload: &types.CompletedMultipartUpload{Parts: []types.CompletedPart{{ETag: aws.String(`"x"`), PartNumber: aws.Int32(1)}}},
				})
				return err
			},
			wantCode: "NoSuchUpload", wantStatus: 404,
		},
		{
			name: "ListParts/NoSuchUpload",
			run: func() error {
				_, err := cl.ListParts(ctx, &s3.ListPartsInput{Bucket: aws.String(bucket), Key: aws.String("x"), UploadId: aws.String("bogus-upload-id")})
				return err
			},
			wantCode: "NoSuchUpload", wantStatus: 404,
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			err := c.run()
			if err == nil {
				t.Fatalf("expected an error, got none")
			}
			if code := testkit.ErrorCode(err); code != c.wantCode {
				t.Errorf("error code: want %s got %s (raw: %v)", c.wantCode, code, err)
			}
			if status := testkit.StatusCode(err); status != c.wantStatus {
				t.Errorf("HTTP status: want %d got %d", c.wantStatus, status)
			}
		})
	}
}

// TestDeleteNonEmptyBucket documents a deliberate SeaweedFS deviation from
// AWS: `weed s3`'s `-allowDeleteBucketNotEmpty` flag defaults to true
// (weed/command/s3.go:118, "allow recursive deleting all entries along
// with bucket"), so DeleteBucket on a non-empty bucket SUCCEEDS and
// recursively deletes its contents by default - AWS returns
// BucketNotEmpty (409) instead. This is dangerous for any application
// that relies on AWS's refusal as a safety net. See docs/compat-notes.md.
//
// This test asserts SeaweedFS's actual default behavior (succeeds,
// contents gone) so a future change to that default - in either
// direction - is caught as a regression, not silently re-validated by a
// test that encoded the wrong assumption.
func TestDeleteNonEmptyBucketRecursivelyDeletesByDefault(t *testing.T) {
	tg := testkit.Target(t)
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "errors")

	key := testkit.Prefix(t) + "occupant"
	if _, err := cl.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader([]byte("x"))}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	_, err := cl.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucket)})
	if err != nil {
		t.Fatalf("DeleteBucket on a non-empty bucket: want success (SeaweedFS default AllowDeleteBucketNotEmpty=true), got error: %v", err)
	}

	if _, err := cl.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)}); err == nil {
		t.Fatal("bucket still exists after DeleteBucket reported success")
	}
}
