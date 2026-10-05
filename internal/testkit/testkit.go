// Package testkit provides per-test bucket/prefix isolation, cleanup and
// small assertion helpers shared by every test package under tests/.
package testkit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/upayanmazumder-DevLabs/seaweedfs-s3-harness/internal/s3x"
	"github.com/upayanmazumder-DevLabs/seaweedfs-s3-harness/internal/target"
)

// runID identifies one invocation of the test binary, so buckets from
// concurrent or repeated runs never collide and the janitor can find them.
var runID = randomHex(6)

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// RunID returns the shared run identifier for this process.
func RunID() string { return runID }

// Target loads the target.Target from the environment once per test,
// skipping the test with a clear message if required variables are absent
// (useful for `go test ./...` without a running target configured).
func Target(t *testing.T) *target.Target {
	t.Helper()
	tg, err := target.FromEnv()
	if err != nil {
		t.Skipf("skipping: %v (run against a target via `make test TARGET=...`)", err)
	}
	return tg
}

// Client returns an Admin S3 client for tg, with a background context that
// is canceled on test cleanup.
func Client(t *testing.T, tg *target.Target) (*s3.Client, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)
	return s3x.New(tg), ctx
}

// Bucket creates an isolated bucket named "it-<run>-<category>-<rand>" and
// registers cleanup: abort any multipart uploads, delete all object
// versions, then delete the bucket. Cleanup runs even on test failure.
func Bucket(t *testing.T, cl *s3.Client, ctx context.Context, category string) string {
	t.Helper()
	name := fmt.Sprintf("it-%s-%s-%s", runID, category, randomHex(4))
	_, err := cl.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(name)})
	if err != nil {
		t.Fatalf("creating bucket %s: %v", name, err)
	}
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := EmptyAndDeleteBucket(cctx, cl, name); err != nil {
			t.Logf("cleanup: bucket %s: %v", name, err)
		}
	})
	return name
}

// Prefix returns a per-test key prefix, so parallel tests sharing a bucket
// never see each other's keys.
func Prefix(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%s/%s/", strings.ReplaceAll(t.Name(), "/", "_"), randomHex(4))
}

// EmptyAndDeleteBucket aborts in-flight multipart uploads, deletes every
// object (and version, if versioning was enabled), then deletes the bucket.
// Used by Bucket's cleanup and by the standalone janitor.
func EmptyAndDeleteBucket(ctx context.Context, cl *s3.Client, bucket string) error {
	// Abort multipart uploads first: a pending upload's parts are not
	// covered by ListObjectsV2 / DeleteObjects.
	mpus, err := cl.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{Bucket: aws.String(bucket)})
	if err == nil {
		for _, u := range mpus.Uploads {
			_, _ = cl.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
				Bucket: aws.String(bucket), Key: u.Key, UploadId: u.UploadId,
			})
		}
	}

	// Try versioned listing first; a never-versioned bucket returns a
	// clean "null" version per key, which DeleteObjects accepts fine.
	var token *string
	for {
		out, err := cl.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{
			Bucket: aws.String(bucket), KeyMarker: token,
		})
		if err != nil {
			break // fall through to the ListObjectsV2 path below
		}
		var ids []types.ObjectIdentifier
		for _, v := range out.Versions {
			ids = append(ids, types.ObjectIdentifier{Key: v.Key, VersionId: v.VersionId})
		}
		for _, m := range out.DeleteMarkers {
			ids = append(ids, types.ObjectIdentifier{Key: m.Key, VersionId: m.VersionId})
		}
		if len(ids) > 0 {
			if _, err := cl.DeleteObjects(ctx, &s3.DeleteObjectsInput{
				Bucket: aws.String(bucket), Delete: &types.Delete{Objects: ids, Quiet: aws.Bool(true)},
			}); err != nil {
				return fmt.Errorf("deleting object versions: %w", err)
			}
		}
		if out.IsTruncated == nil || !*out.IsTruncated {
			break
		}
		token = out.NextKeyMarker
	}

	// Plain listing fallback / sweep for anything versioning missed.
	var contToken *string
	for {
		out, err := cl.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket: aws.String(bucket), ContinuationToken: contToken,
		})
		if err != nil {
			break
		}
		if len(out.Contents) > 0 {
			var ids []types.ObjectIdentifier
			for _, o := range out.Contents {
				ids = append(ids, types.ObjectIdentifier{Key: o.Key})
			}
			if _, err := cl.DeleteObjects(ctx, &s3.DeleteObjectsInput{
				Bucket: aws.String(bucket), Delete: &types.Delete{Objects: ids, Quiet: aws.Bool(true)},
			}); err != nil {
				return fmt.Errorf("deleting objects: %w", err)
			}
		}
		if out.IsTruncated == nil || !*out.IsTruncated {
			break
		}
		contToken = out.NextContinuationToken
	}

	_, err = cl.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucket)})
	return err
}

// ErrorCode extracts the S3 error Code from err, or "" if err is not an S3
// API error. Use this instead of string-matching err.Error().
func ErrorCode(err error) string {
	var apiErr smithy_APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode()
	}
	return ""
}

// smithy_APIError aliases the smithy interface so callers do not need to
// import smithy directly.
type smithy_APIError interface {
	error
	ErrorCode() string
	ErrorMessage() string
}

// StatusCode extracts the HTTP status code from an S3 API error, or 0.
func StatusCode(err error) int {
	var re *smithyhttp.ResponseError
	if errors.As(err, &re) {
		return re.HTTPStatusCode()
	}
	return 0
}

// SkipUnlessMultiIdentity skips the test when the target has no
// ReadOnly/Scoped credentials configured.
func SkipUnlessMultiIdentity(t *testing.T, tg *target.Target) {
	t.Helper()
	if !tg.Capabilities.MultiIdentity {
		t.Skip("target has no SWFSIT_RO_*/SWFSIT_SCOPED_* credentials configured")
	}
}

// SkipUnlessVersioning skips the test when the target does not claim
// versioning support.
func SkipUnlessVersioning(t *testing.T, tg *target.Target) {
	t.Helper()
	if !tg.Capabilities.Versioning {
		t.Skip("target capabilities do not include versioning")
	}
}

// MaxObjectBytes returns the target's configured object-size ceiling.
func MaxObjectBytes(tg *target.Target) int64 {
	return tg.Capabilities.MaxObjectMiB * 1024 * 1024
}
