// Command vcapsim is a small, intentionally minimal stand-in for VCAP's
// real storage usage pattern (see docs/vcap-mapping.md for the source
// mapping). It exercises the same operations VCAP's backend performs
// against object storage - PUT a workspace archive, "compile" it, PUT the
// result artifacts, DELETE the workspace - so the harness can assert on a
// realistic workload, not just raw S3 calls.
//
// vcapsim is not a web service: for the harness's purposes, the workflow
// logic is what matters, not a HTTP/queue layer, so it runs as a single
// synchronous pass over a batch of simulated submissions. This keeps the
// harness's own footprint small while still exercising VCAP's real
// bucket/key layout and content types.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/upayanmazumder-DevLabs/seaweedfs-s3-harness/internal/payload"
	"github.com/upayanmazumder-DevLabs/seaweedfs-s3-harness/internal/s3x"
	"github.com/upayanmazumder-DevLabs/seaweedfs-s3-harness/internal/target"
)

func main() {
	n := flag.Int("submissions", 10, "number of simulated submissions to run")
	workspaceBucket := flag.String("workspace-bucket", "vcap-workspace", "bucket for uploaded ZIPs, mirrors VCAP's BUCKET_WORKSPACE")
	artifactsBucket := flag.String("artifacts-bucket", "vcap-artifacts", "bucket for compiled output, mirrors VCAP's BUCKET_ARTIFACTS")
	flag.Parse()

	tg, err := target.FromEnv()
	if err != nil {
		log.Fatalf("loading target: %v", err)
	}
	cl := s3x.New(tg)
	ctx := context.Background()

	for _, b := range []string{*workspaceBucket, *artifactsBucket} {
		if _, err := cl.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(b)}); err != nil {
			log.Printf("CreateBucket(%s): %v (continuing; may already exist)", b, err)
		}
	}

	var failures int
	for i := 0; i < *n; i++ {
		if err := runSubmission(ctx, cl, *workspaceBucket, *artifactsBucket, i); err != nil {
			log.Printf("submission %d: FAIL: %v", i, err)
			failures++
			continue
		}
		log.Printf("submission %d: OK", i)
	}

	fmt.Printf("vcapsim: %d/%d submissions succeeded\n", *n-failures, *n)
	if failures > 0 {
		os.Exit(1)
	}
}

// runSubmission mirrors VCAP's real flow, with file paths taken from the
// VCAP source: upload.go (workspace key = task UUID, Content-Type
// application/zip), compile.go (artifacts at <project>/<task>/{name.pdf,
// main.log}), and upload.go's cleanup delete of the workspace object.
func runSubmission(ctx context.Context, cl *s3.Client, workspaceBucket, artifactsBucket string, seed int) error {
	taskID := fmt.Sprintf("task-%d-%d", time.Now().UnixNano(), seed)
	projectID := fmt.Sprintf("project-%d", seed%5) // several tasks share a project, like real courses

	// 1. Submit: PUT the workspace ZIP.
	zipSize := int64(1+rand.IntN(30)) * 1024 * 1024 // 1-30 MiB, VCAP's real range
	zip := payload.New(int64(seed), zipSize, payload.Random)
	workspaceKey := taskID
	if _, err := cl.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(workspaceBucket), Key: aws.String(workspaceKey),
		Body: zip.Reader(), ContentType: aws.String("application/zip"),
	}); err != nil {
		return fmt.Errorf("uploading workspace: %w", err)
	}

	// 2. Worker: download the ZIP, "compile" it (a deterministic
	// transform so the test can predict the output without re-running
	// vcapsim), upload the resulting PDF and log.
	out, err := cl.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(workspaceBucket), Key: aws.String(workspaceKey)})
	if err != nil {
		return fmt.Errorf("downloading workspace: %w", err)
	}
	h := sha256.New()
	if _, err := io.Copy(h, out.Body); err != nil {
		out.Body.Close()
		return fmt.Errorf("reading workspace: %w", err)
	}
	out.Body.Close()
	workspaceDigest := h.Sum(nil)

	pdf := compile(workspaceDigest)
	pdfKey := fmt.Sprintf("%s/%s/report.pdf", projectID, taskID)
	if _, err := cl.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(artifactsBucket), Key: aws.String(pdfKey),
		Body: bytes.NewReader(pdf), ContentType: aws.String("application/pdf"),
	}); err != nil {
		return fmt.Errorf("uploading report.pdf: %w", err)
	}

	logKey := fmt.Sprintf("%s/%s/main.log", projectID, taskID)
	logBody := []byte(fmt.Sprintf("compiled task %s from workspace sha256:%x\n", taskID, workspaceDigest))
	if _, err := cl.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(artifactsBucket), Key: aws.String(logKey),
		Body: bytes.NewReader(logBody), ContentType: aws.String("text/plain"),
	}); err != nil {
		return fmt.Errorf("uploading main.log: %w", err)
	}

	// 3. Cleanup: delete the workspace object, like VCAP's
	// upload.go cleanup path.
	if _, err := cl.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(workspaceBucket), Key: aws.String(workspaceKey)}); err != nil {
		return fmt.Errorf("deleting workspace: %w", err)
	}

	// 4. Verify: download the artifacts back and confirm they match
	// what "compile" produced, and that the workspace key is gone.
	gotPDF, err := cl.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(artifactsBucket), Key: aws.String(pdfKey)})
	if err != nil {
		return fmt.Errorf("downloading report.pdf: %w", err)
	}
	defer gotPDF.Body.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, gotPDF.Body); err != nil {
		return fmt.Errorf("reading report.pdf: %w", err)
	}
	if !bytes.Equal(buf.Bytes(), pdf) {
		return fmt.Errorf("report.pdf content mismatch after roundtrip")
	}

	if _, err := cl.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(workspaceBucket), Key: aws.String(workspaceKey)}); err == nil {
		return fmt.Errorf("workspace object %s still present after cleanup delete", workspaceKey)
	}

	return nil
}

// compile is a deterministic, content-addressed stand-in for a real LaTeX
// build: the "PDF" is simply a function of the workspace's digest, so a
// test can predict and verify it without re-implementing LaTeX.
func compile(workspaceDigest []byte) []byte {
	h := sha256.Sum256(append([]byte("vcapsim-compiled:"), workspaceDigest...))
	return append([]byte("%PDF-1.4 vcapsim-synthetic\n"), h[:]...)
}
