// Package payload generates deterministic, seekable test payloads and
// verifies downloaded bytes against them by digest, not just by length.
package payload

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
)

// Pattern selects the byte content of a generated payload.
type Pattern int

const (
	// Random is an AES-CTR keystream, seeded, so it is reproducible but
	// not compressible or all-zero (catches sparse-file / dedup shortcuts).
	Random Pattern = iota
	// Zero is all zero bytes (catches sparse-file handling).
	Zero
	// Cycle repeats 0x00..0xFF (catches off-by-one chunk boundary bugs
	// because every byte offset is distinguishable mod 256).
	Cycle
)

func (p Pattern) String() string {
	switch p {
	case Random:
		return "random"
	case Zero:
		return "zero"
	case Cycle:
		return "cycle"
	default:
		return "unknown"
	}
}

// Payload is a deterministic, re-readable byte source plus its expected digests.
type Payload struct {
	Seed    int64
	Size    int64
	Pattern Pattern

	md5    string
	sha256 string
	sha1   string
	crc32  uint32
}

// New builds a Payload of the given size and pattern. Generation is a single
// pass to compute digests; Reader() returns a fresh, independent, seekable
// reader each time it is called.
func New(seed int64, size int64, pattern Pattern) *Payload {
	p := &Payload{Seed: seed, Size: size, Pattern: pattern}

	h5 := md5.New()
	h256 := sha256.New()
	h1 := sha1.New()
	c32 := crc32.NewIEEE()
	mw := io.MultiWriter(h5, h256, h1, hashWriter{c32})

	r := p.newRawReader()
	if _, err := io.Copy(mw, r); err != nil {
		panic(fmt.Sprintf("payload: digest pass failed: %v", err))
	}

	p.md5 = hex.EncodeToString(h5.Sum(nil))
	p.sha256 = hex.EncodeToString(h256.Sum(nil))
	p.sha1 = hex.EncodeToString(h1.Sum(nil))
	p.crc32 = c32.Sum32()
	return p
}

type hashWriter struct{ h hash.Hash32 }

func (w hashWriter) Write(b []byte) (int, error) { return w.h.Write(b) }

// Reader returns a fresh io.ReadSeeker over the payload bytes. Safe to call
// multiple times and from multiple goroutines concurrently (each call gets
// an independent reader).
func (p *Payload) Reader() io.ReadSeeker { return p.newRawReader() }

// MD5 returns the lowercase hex MD5 digest of the full payload.
func (p *Payload) MD5() string { return p.md5 }

// SHA256 returns the lowercase hex SHA-256 digest of the full payload.
func (p *Payload) SHA256() string { return p.sha256 }

// SHA1 returns the lowercase hex SHA-1 digest of the full payload.
func (p *Payload) SHA1() string { return p.sha1 }

// CRC32 returns the IEEE CRC-32 of the full payload.
func (p *Payload) CRC32() uint32 { return p.crc32 }

// ETag returns the plain-PUT S3 ETag: a quoted hex MD5.
func (p *Payload) ETag() string { return `"` + p.md5 + `"` }

// Describe returns a short, log-friendly identity for failure messages.
func (p *Payload) Describe() string {
	return fmt.Sprintf("seed=%d size=%d pattern=%s md5=%s", p.Seed, p.Size, p.Pattern, p.md5[:12])
}

// newRawReader returns a deterministic ReadSeeker without touching digests.
func (p *Payload) newRawReader() io.ReadSeeker {
	switch p.Pattern {
	case Zero:
		return io.NewSectionReader(zeroReaderAt{}, 0, p.Size)
	case Cycle:
		return io.NewSectionReader(cycleReaderAt{}, 0, p.Size)
	default:
		return io.NewSectionReader(&ctrReaderAt{seed: p.Seed}, 0, p.Size)
	}
}

type zeroReaderAt struct{}

func (zeroReaderAt) ReadAt(b []byte, off int64) (int, error) {
	for i := range b {
		b[i] = 0
	}
	return len(b), nil
}

type cycleReaderAt struct{}

func (cycleReaderAt) ReadAt(b []byte, off int64) (int, error) {
	for i := range b {
		b[i] = byte((off + int64(i)) % 256)
	}
	return len(b), nil
}

// ctrReaderAt is an AES-CTR keystream reader keyed by seed. It supports
// random access ReadAt by seeking the keystream counter, which keeps Reader()
// cheap even for 100 MiB payloads (no buffering of the whole object).
type ctrReaderAt struct {
	seed int64
}

func (r *ctrReaderAt) ReadAt(b []byte, off int64) (int, error) {
	var key [32]byte
	binary.BigEndian.PutUint64(key[:8], uint64(r.seed))
	binary.BigEndian.PutUint64(key[8:16], uint64(r.seed)^0x5eaf00d5eaf00d)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return 0, err
	}
	blockSize := int64(block.BlockSize())
	blockOffset := off / blockSize
	within := int(off % blockSize)

	var iv [16]byte
	// Use the seed as the IV base so every size/pattern-offset combination
	// has an independent keystream start, then advance the counter to the
	// requested block.
	binary.BigEndian.PutUint64(iv[8:], uint64(blockOffset))

	stream := cipher.NewCTR(block, iv[:])
	buf := make([]byte, within+len(b))
	stream.XORKeyStream(buf, buf)
	n := copy(b, buf[within:])
	return n, nil
}

// VerifyReader reads all of r, computing the same digests, and returns a
// non-nil error describing exactly where the first mismatch is (not just
// "bytes differ") if the content does not match p.
func (p *Payload) VerifyReader(r io.Reader) error {
	var buf bytes.Buffer
	n, err := io.Copy(&buf, r)
	if err != nil {
		return fmt.Errorf("reading actual bytes: %w", err)
	}
	if n != p.Size {
		return fmt.Errorf("length mismatch: want %d got %d", p.Size, n)
	}
	got := sha256.Sum256(buf.Bytes())
	gotHex := hex.EncodeToString(got[:])
	if gotHex != p.sha256 {
		off := firstDiffOffset(p, buf.Bytes())
		return fmt.Errorf("sha256 mismatch: want %s got %s (first difference at byte offset %d)", p.sha256, gotHex, off)
	}
	return nil
}

// firstDiffOffset re-reads the expected payload and returns the first byte
// offset where it differs from got. Used only on verification failure, so
// the extra pass is acceptable.
func firstDiffOffset(p *Payload, got []byte) int64 {
	n := io.NewSectionReader(sectionerFor(p), 0, p.Size)
	full := make([]byte, p.Size)
	_, _ = io.ReadFull(n, full)
	for i := range full {
		if i >= len(got) || full[i] != got[i] {
			return int64(i)
		}
	}
	return -1
}

func sectionerFor(p *Payload) io.ReaderAt {
	switch p.Pattern {
	case Zero:
		return zeroReaderAt{}
	case Cycle:
		return cycleReaderAt{}
	default:
		return &ctrReaderAt{seed: p.Seed}
	}
}

var _ io.ReaderAt = (*ctrReaderAt)(nil)
