// Package basic covers PUT/GET/HEAD/DELETE/LIST fundamentals: the matrix
// IDs B01-B13 in docs/test-matrix.md.
package basic

import (
	"bytes"
	"io"
	"strconv"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/upayanmazumder-DevLabs/seaweedfs-s3-harness/internal/payload"
	"github.com/upayanmazumder-DevLabs/seaweedfs-s3-harness/internal/testkit"
)

// B01: PUT then GET must return exactly the bytes written, verified by
// SHA-256, not merely by status code or Content-Length.
func TestPutGetRoundtrip(t *testing.T) {
	tg := testkit.Target(t)
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "basic")
	key := testkit.Prefix(t) + "roundtrip"

	p := payload.New(1, 64*1024, payload.Random)
	if _, err := cl.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: p.Reader(),
	}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	out, err := cl.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	defer out.Body.Close()

	if err := p.VerifyReader(out.Body); err != nil {
		t.Fatalf("downloaded bytes do not match upload (%s): %v", p.Describe(), err)
	}
	if out.ETag == nil || *out.ETag != p.ETag() {
		t.Fatalf("ETag: want %s got %v", p.ETag(), out.ETag)
	}
}

// B02: HEAD must report the same Content-Length, ETag and Content-Type as
// GET, and a Last-Modified header must be present.
func TestHeadMatchesGet(t *testing.T) {
	tg := testkit.Target(t)
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "basic")
	key := testkit.Prefix(t) + "head"

	p := payload.New(2, 2048, payload.Cycle)
	if _, err := cl.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: p.Reader(),
		ContentType: aws.String("application/octet-stream"),
	}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	head, err := cl.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		t.Fatalf("HeadObject: %v", err)
	}
	get, err := cl.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	defer get.Body.Close()
	_, _ = io.Copy(io.Discard, get.Body)

	if aws.ToInt64(head.ContentLength) != p.Size {
		t.Errorf("HEAD Content-Length: want %d got %d", p.Size, aws.ToInt64(head.ContentLength))
	}
	if aws.ToString(head.ETag) != aws.ToString(get.ETag) {
		t.Errorf("ETag mismatch: HEAD=%s GET=%s", aws.ToString(head.ETag), aws.ToString(get.ETag))
	}
	if aws.ToString(head.ContentType) != aws.ToString(get.ContentType) {
		t.Errorf("Content-Type mismatch: HEAD=%s GET=%s", aws.ToString(head.ContentType), aws.ToString(get.ContentType))
	}
	if head.LastModified == nil {
		t.Error("HEAD Last-Modified is missing")
	}
}

// B03: GET and HEAD on a missing key must both fail, and HEAD must not
// return a body (the SDK call itself proves this: HeadObjectOutput has no
// Body field).
func TestMissingKey(t *testing.T) {
	tg := testkit.Target(t)
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "basic")
	key := testkit.Prefix(t) + "does-not-exist"

	_, err := cl.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err == nil {
		t.Fatal("GetObject on a missing key succeeded")
	}
	if code := testkit.ErrorCode(err); code != "NoSuchKey" {
		t.Errorf("GetObject error code: want NoSuchKey got %q (status %d)", code, testkit.StatusCode(err))
	}

	_, err = cl.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err == nil {
		t.Fatal("HeadObject on a missing key succeeded")
	}
	if sc := testkit.StatusCode(err); sc != 404 {
		t.Errorf("HeadObject status: want 404 got %d", sc)
	}
}

// B04: operations against a bucket that was never created must fail with
// NoSuchBucket, not some other error.
func TestMissingBucket(t *testing.T) {
	tg := testkit.Target(t)
	cl, ctx := testkit.Client(t, tg)
	bucket := "it-" + testkit.RunID() + "-never-created"

	_, err := cl.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String("x")})
	if err == nil {
		t.Fatal("GetObject on a missing bucket succeeded")
	}
	if code := testkit.ErrorCode(err); code != "NoSuchBucket" {
		t.Errorf("error code: want NoSuchBucket got %q", code)
	}
}

// B05: DELETE must make a subsequent GET fail with NoSuchKey.
func TestDeleteThenGet(t *testing.T) {
	tg := testkit.Target(t)
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "basic")
	key := testkit.Prefix(t) + "to-delete"

	if _, err := cl.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader([]byte("x")),
	}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	if _, err := cl.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)}); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
	_, err := cl.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err == nil {
		t.Fatal("GetObject succeeded after DeleteObject")
	}
	if code := testkit.ErrorCode(err); code != "NoSuchKey" {
		t.Errorf("error code after delete: want NoSuchKey got %q", code)
	}
}

// B06: DeleteObject on a key that was never created must still return
// success (204), matching AWS semantics.
func TestDeleteMissingKey(t *testing.T) {
	tg := testkit.Target(t)
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "basic")

	_, err := cl.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(testkit.Prefix(t) + "never-existed"),
	})
	if err != nil {
		t.Fatalf("DeleteObject on a missing key should succeed: %v", err)
	}
}

// B07: repeatedly overwriting a key must always leave exactly the latest
// write's bytes visible, with no concatenation, truncation or stale tail
// from a previous, larger version.
func TestOverwriteSequence(t *testing.T) {
	tg := testkit.Target(t)
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "basic")
	key := testkit.Prefix(t) + "overwrite"

	sizes := []int64{5 * 1024 * 1024, 100, 5 * 1024 * 1024}
	var lastETag string
	for i, size := range sizes {
		p := payload.New(int64(100+i), size, payload.Random)
		put, err := cl.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(bucket), Key: aws.String(key), Body: p.Reader(),
		})
		if err != nil {
			t.Fatalf("PutObject #%d (size %d): %v", i, size, err)
		}
		if put.ETag != nil {
			if *put.ETag == lastETag {
				t.Errorf("PutObject #%d: ETag did not change after overwrite", i)
			}
			lastETag = *put.ETag
		}

		out, err := cl.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
		if err != nil {
			t.Fatalf("GetObject after write #%d: %v", i, err)
		}
		if err := p.VerifyReader(out.Body); err != nil {
			out.Body.Close()
			t.Fatalf("after write #%d (size %d): %v", i, size, err)
		}
		out.Body.Close()
	}
}

// B08: a zero-byte object must round-trip as zero bytes with the
// well-known empty-MD5 ETag.
func TestEmptyObject(t *testing.T) {
	tg := testkit.Target(t)
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "basic")
	key := testkit.Prefix(t) + "empty"

	put, err := cl.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader(nil),
	})
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	const emptyETag = `"d41d8cd98f00b204e9800998ecf8427e"`
	if put.ETag == nil || *put.ETag != emptyETag {
		t.Errorf("ETag: want %s got %v", emptyETag, put.ETag)
	}

	out, err := cl.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	defer out.Body.Close()
	b, _ := io.ReadAll(out.Body)
	if len(b) != 0 {
		t.Errorf("expected 0 bytes, got %d", len(b))
	}
	if aws.ToInt64(out.ContentLength) != 0 {
		t.Errorf("Content-Length: want 0 got %d", aws.ToInt64(out.ContentLength))
	}
}

// B09: keys with spaces, unicode, and reserved-looking characters must
// round-trip exactly and be visible, verbatim, in a listing.
func TestSpecialKeys(t *testing.T) {
	tg := testkit.Target(t)
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "basic")
	prefix := testkit.Prefix(t)

	keys := []string{
		"with space",
		"unicode-\u00e9\u00e8\u4e2d\u6587",
		"plus+percent%25equals=and&tilde~bang!quote'paren()comma,",
		"nested/deeper/path",
		"projects/42/review-1/v3/report.pdf", // VCAP's real artifact layout
	}

	for _, k := range keys {
		k := k
		t.Run(k, func(t *testing.T) {
			full := prefix + k
			p := payload.New(int64(len(k)), 128, payload.Random)
			if _, err := cl.PutObject(ctx, &s3.PutObjectInput{
				Bucket: aws.String(bucket), Key: aws.String(full), Body: p.Reader(),
			}); err != nil {
				t.Fatalf("PutObject(%q): %v", full, err)
			}
			out, err := cl.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(full)})
			if err != nil {
				t.Fatalf("GetObject(%q): %v", full, err)
			}
			defer out.Body.Close()
			if err := p.VerifyReader(out.Body); err != nil {
				t.Fatalf("GetObject(%q): %v", full, err)
			}
		})
	}

	list, err := cl.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket), Prefix: aws.String(prefix)})
	if err != nil {
		t.Fatalf("ListObjectsV2: %v", err)
	}
	seen := map[string]bool{}
	for _, o := range list.Contents {
		seen[aws.ToString(o.Key)] = true
	}
	for _, k := range keys {
		if !seen[prefix+k] {
			t.Errorf("key %q not present verbatim in ListObjectsV2 (saw %d keys)", prefix+k, len(list.Contents))
		}
	}
}

// B11: DeleteObjects must report every requested key as deleted, including
// keys that never existed, with no per-key errors.
func TestDeleteObjectsBatch(t *testing.T) {
	tg := testkit.Target(t)
	cl, ctx := testkit.Client(t, tg)
	bucket := testkit.Bucket(t, cl, ctx, "basic")
	prefix := testkit.Prefix(t)

	const n = 50
	var ids []types.ObjectIdentifier
	for i := 0; i < n; i++ {
		key := prefix + "k" + strconv.Itoa(i)
		if _, err := cl.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader([]byte("x")),
		}); err != nil {
			t.Fatalf("PutObject(%d): %v", i, err)
		}
		ids = append(ids, types.ObjectIdentifier{Key: aws.String(key)})
	}
	// Include a key that was never created.
	ids = append(ids, types.ObjectIdentifier{Key: aws.String(prefix + "missing")})

	out, err := cl.DeleteObjects(ctx, &s3.DeleteObjectsInput{
		Bucket: aws.String(bucket),
		Delete: &types.Delete{Objects: ids, Quiet: aws.Bool(false)},
	})
	if err != nil {
		t.Fatalf("DeleteObjects: %v", err)
	}
	if len(out.Errors) != 0 {
		t.Errorf("DeleteObjects reported %d per-key errors, want 0: %+v", len(out.Errors), out.Errors)
	}
	if len(out.Deleted) != n+1 {
		t.Errorf("DeleteObjects: want %d Deleted entries, got %d", n+1, len(out.Deleted))
	}
}
