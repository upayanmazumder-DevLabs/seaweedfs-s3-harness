package payload

import (
	"bytes"
	"io"
	"testing"
)

func TestDeterministic(t *testing.T) {
	for _, pat := range []Pattern{Random, Zero, Cycle} {
		a := New(42, 70000, pat) // > one AES block, > typical read buffer
		b := New(42, 70000, pat)
		if a.SHA256() != b.SHA256() {
			t.Fatalf("%s: same seed/size produced different digests", pat)
		}
	}
	// Only Random is keyed by seed; Zero and Cycle are pure functions of
	// offset and size, so different seeds are expected to collide there.
	c1 := New(42, 70000, Random)
	c2 := New(43, 70000, Random)
	if c1.SHA256() == c2.SHA256() {
		t.Fatal("random: different seeds produced identical digests")
	}
}

func TestReaderMatchesDigest(t *testing.T) {
	p := New(7, 1<<20+37, Random) // not block-aligned
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, p.Reader()); err != nil {
		t.Fatal(err)
	}
	if err := p.VerifyReader(bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("self-verification failed: %v", err)
	}
}

func TestVerifyDetectsCorruption(t *testing.T) {
	p := New(1, 4096, Cycle)
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, p.Reader())
	corrupted := buf.Bytes()
	corrupted[1000] ^= 0xFF
	err := p.VerifyReader(bytes.NewReader(corrupted))
	if err == nil {
		t.Fatal("expected a mismatch error, got nil")
	}
}

func TestReaderIsSeekable(t *testing.T) {
	p := New(9, 1<<20, Random)
	r := p.Reader()
	full := make([]byte, p.Size)
	if _, err := io.ReadFull(r, full); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Seek(100, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	tail := make([]byte, 50)
	if _, err := io.ReadFull(r, tail); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(tail, full[100:150]) {
		t.Fatal("seek+read did not match the equivalent slice of a full read")
	}
}

func TestCycleBytesAreOffsetMod256(t *testing.T) {
	p := New(0, 1000, Cycle)
	full := make([]byte, 1000)
	_, _ = io.ReadFull(p.Reader(), full)
	for i, b := range full {
		if b != byte(i%256) {
			t.Fatalf("offset %d: want %d got %d", i, i%256, b)
		}
	}
}

func TestZeroPayloadDigestIsEmptyDigest(t *testing.T) {
	p := New(0, 0, Random)
	if p.Size != 0 {
		t.Fatal("expected size 0")
	}
	if p.MD5() != "d41d8cd98f00b204e9800998ecf8427e" {
		t.Fatalf("empty payload MD5 should be the well-known empty digest, got %s", p.MD5())
	}
}
