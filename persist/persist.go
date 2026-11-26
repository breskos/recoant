// Package persist saves and loads stores and models in JSON or binary.
//
// JSON files are readable and diffable: an envelope with the format
// version, the kind of content and the data. Binary files are smaller and
// faster for large stores: a header ("RCNT", version, kind) followed by the
// data in encoding/gob. [Load] functions detect the format themselves, so
// the choice only matters when saving.
//
// Files are written to a temporary file first and renamed, so a crash never
// leaves a half-written file behind.
package persist

import (
	"bufio"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Format selects the file format.
type Format int

// Formats.
const (
	JSON Format = iota + 1
	Binary
)

// String returns "json" or "binary".
func (f Format) String() string {
	switch f {
	case JSON:
		return "json"
	case Binary:
		return "binary"
	}
	return fmt.Sprintf("Format(%d)", int(f))
}

// Version is the version of the file layout.
const Version = 1

const magic = "RCNT"

var (
	// ErrFormat is returned for unknown formats and unreadable files.
	ErrFormat = errors.New("persist: unknown or invalid format")
	// ErrKind is returned when a file holds a different kind of content.
	ErrKind = errors.New("persist: wrong kind of content")
	// ErrVersion is returned for files written by a newer layout.
	ErrVersion = errors.New("persist: unsupported version")
)

type envelope struct {
	Recoant int             `json:"recoant"`
	Kind    string          `json:"kind"`
	Data    json.RawMessage `json:"data"`
}

// Encode writes v as content of the given kind.
func Encode(w io.Writer, f Format, kind string, v any) error {
	switch f {
	case JSON:
		data, err := json.Marshal(v)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(w)
		return enc.Encode(envelope{Recoant: Version, Kind: kind, Data: data})
	case Binary:
		if len(kind) > 255 {
			return fmt.Errorf("persist: kind too long")
		}
		hdr := append([]byte(magic), Version, byte(len(kind)))
		hdr = append(hdr, kind...)
		if _, err := w.Write(hdr); err != nil {
			return err
		}
		return gob.NewEncoder(w).Encode(v)
	}
	return fmt.Errorf("%w: %v", ErrFormat, f)
}

// Decode reads content of the given kind into v and returns the format it
// was written in.
func Decode(r io.Reader, kind string, v any) (Format, error) {
	br := bufio.NewReader(r)
	head, err := br.Peek(len(magic))
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrFormat, err)
	}
	if string(head) == magic {
		return Binary, decodeBinary(br, kind, v)
	}
	var env envelope
	if err := json.NewDecoder(br).Decode(&env); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrFormat, err)
	}
	if env.Recoant < 1 || env.Recoant > Version {
		return 0, fmt.Errorf("%w: %d", ErrVersion, env.Recoant)
	}
	if env.Kind != kind {
		return 0, fmt.Errorf("%w: %q, want %q", ErrKind, env.Kind, kind)
	}
	return JSON, json.Unmarshal(env.Data, v)
}

func decodeBinary(br *bufio.Reader, kind string, v any) error {
	var hdr [len(magic) + 2]byte
	if _, err := io.ReadFull(br, hdr[:]); err != nil {
		return fmt.Errorf("%w: %w", ErrFormat, err)
	}
	if ver := hdr[len(magic)]; ver < 1 || ver > Version {
		return fmt.Errorf("%w: %d", ErrVersion, ver)
	}
	k := make([]byte, hdr[len(magic)+1])
	if _, err := io.ReadFull(br, k); err != nil {
		return fmt.Errorf("%w: %w", ErrFormat, err)
	}
	if string(k) != kind {
		return fmt.Errorf("%w: %q, want %q", ErrKind, k, kind)
	}
	if err := gob.NewDecoder(br).Decode(v); err != nil {
		return fmt.Errorf("%w: %w", ErrFormat, err)
	}
	return nil
}

// Save writes v to path atomically.
func Save(path string, f Format, kind string, v any) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	bw := bufio.NewWriterSize(tmp, 1<<20)
	if err = Encode(bw, f, kind, v); err != nil {
		return err
	}
	err = errors.Join(bw.Flush(), tmp.Close())
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	return err
}

// Load reads content of the given kind from path into v.
func Load(path, kind string, v any) (Format, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	return Decode(f, kind, v)
}
