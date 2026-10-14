package ccsid

import (
	"bufio"
	"compress/gzip"
	"encoding/hex"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
)

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	if s == "-" {
		return nil
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

// TestJTOpenGolden compares conversions against vectors produced by JTOpen
// (gen/CcsidGolden.java).
func TestJTOpenGolden(t *testing.T) {
	f, err := os.Open("testdata/jtopen_golden.txt.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	seen := map[int]bool{}
	failures := 0
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 4 {
			t.Fatalf("bad golden line %q", sc.Text())
		}
		id, _ := strconv.Atoi(fields[1])
		seen[id] = true
		in, want := unhex(t, fields[2]), unhex(t, fields[3])
		var got []byte
		switch fields[0] {
		case "E":
			got, err = Encode(string(in), id)
		case "D":
			var s string
			s, err = Decode(in, id)
			got = []byte(s)
		}
		if err != nil {
			t.Errorf("%s %d %x: %v", fields[0], id, in, err)
			failures++
		} else if string(got) != string(want) {
			t.Errorf("%s %d %x:\n got %x\nwant %x", fields[0], id, in, got, want)
			failures++
		}
		if failures > 20 {
			t.Fatal("too many failures")
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	for _, id := range List() {
		if !seen[id] {
			t.Errorf("CCSID %d has no golden vectors", id)
		}
	}
}

func TestListAndSupported(t *testing.T) {
	ids := List()
	if len(ids) < 150 {
		t.Fatalf("List() = %d CCSIDs, want >= 150", len(ids))
	}
	for _, id := range ids {
		if !Supported(id) {
			t.Errorf("Supported(%d) = false", id)
		}
		if _, err := Lookup(id); err != nil {
			t.Errorf("Lookup(%d): %v", id, err)
		}
	}
	if _, err := Lookup(5000); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Lookup(5000) error = %v, want ErrUnsupported", err)
	}
}

func TestSpace(t *testing.T) {
	cases := map[int]string{37: "40", 819: "20", 1200: "0020", 1208: "20", 930: "40", 300: "4040", 1202: "2000"}
	for id, want := range cases {
		c, err := Lookup(id)
		if err != nil {
			t.Fatal(err)
		}
		if got := hex.EncodeToString(c.Space()); got != want {
			t.Errorf("Space(%d) = %s, want %s", id, got, want)
		}
	}
}
