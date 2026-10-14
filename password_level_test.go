package i400

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// Golden vectors for the password substitute. Each "want" value is the output of JTOpen
// AS400ImplRemote.getPassword(clientSeed, serverSeed) for the same user, password and
// password level, run with the fixed seeds below. Levels 0-1 are DES (8 bytes), 2-3 are
// SHA-1 (20 bytes), and 4 is PBKDF2-HMAC-SHA512 plus SHA-512 (64 bytes).
var (
	goldenClientSeed = []byte{1, 2, 3, 4, 5, 6, 7, 8}
	goldenServerSeed = []byte{0xA1, 0xB2, 0xC3, 0xD4, 0xE5, 0xF6, 0x07, 0x18}
)

var passwordSubstituteGolden = []struct {
	level    int
	user     string
	password string
	want     string
	wantErr  bool
}{
	{level: 0, user: "QSECOFR", password: "password", want: "3f0122a5f35d6cb0"},
	{level: 1, user: "QSECOFR", password: "password", want: "3f0122a5f35d6cb0"},
	{level: 0, user: "JOE", password: "Pa55w0rd", want: "6309658170713d2f"},
	{level: 1, user: "JOE", password: "1234abcd", want: "4919f06382377bc7"},
	{level: 0, user: "QSECOFR", password: "ABCDEFGHIJ", want: "bd9d965bb0750562"},
	{level: 1, user: "QSECOFR", password: "MixedCase9", want: "230a4ab2d4ed5196"},
	{level: 2, user: "QSECOFR", password: "Secret1", want: "dad7121b1985d5ea5477cb33e2ff037cfca28d51"},
	{level: 3, user: "QSECOFR", password: "Secret1   ", want: "dad7121b1985d5ea5477cb33e2ff037cfca28d51"},
	{level: 2, user: "QSECOFR", password: "Secret1\u0000 ", want: "dad7121b1985d5ea5477cb33e2ff037cfca28d51"},
	{level: 3, user: "JOE", password: "LongerPassword!2026", want: "4c23438fa0c78b2783ba1d056dee6a2f7acde79c"},
	{level: 2, user: "QSECOFR", password: "Ab\u3000 ", want: "087b4dc0037be3cb0068679ce044ea646ce63e7c"},
	{level: 4, user: "QSECOFR", password: "Secret1", want: "ca523d59c9b8820eb3687f0b6670ab550be19e0037a921ecf11f4996bfe95857dc11232d85fba335749a13715d0577767bdec2c0e5cd588b030f5408d8e1b458"},
	{level: 4, user: "QSECOFR", password: "Secret1   ", want: "d4a94f62333abdd7613f30f0e0411fc813125f38d0b109f3ddfd5e6a3b4d7f1589936cf807b5ca64986bf0bcc87e21039fd686f8335a29db137c9dbaa46f5414"},
	{level: 4, user: "JOE", password: "ab", want: "f3bf2bda30381ebb1984797c6469b9f5cf689101186d65867452e11b701b8e180a89042e592fc7f309c8df0abdb8896d6e619d30ed2ccf8fc917851471c05a0f"},
	{level: 4, user: "QSECOFR", password: "LongerPassword!2026", want: "43ca215acd4f533df4a9454e35bf41a2dbb9e5fb2115e20350cd36df6e2e858104152d641a232a80d73561a7ac9162681e8bd6b987570f9c635b25b248e5d5cd"},
	{level: 4, user: "QSECOFR", password: "Pass word", want: "ce02e37b87fade1ff74585d877ca236d861024e23d51a8f96772ab3fb06008ad85ad3f3d87f821e714fc94f51ac15edfeb2183bdc1fad2300726a725d421512b"},
	{level: 4, user: "JOE", password: "1234abcd", want: "31462bb2def940cd909b6869a58cf3a58706862581d00cd4e0d75bc21915bd936faf12de00598c82b0ef42f09bf3df5e81a0e8bb701c8494b4b0978dfe994bd1"},
	{level: 4, user: "QSECOFR", password: "Secret1\u0000", want: "4a12f9c4e17860f326e4398b1922344214c309c228fa0c5b1fc76c01bfff96d41f49dafcc3c24fb6357a67b458a9df13dccb6e6844aa1de44be851f75c98d9a4"},
	{level: 4, user: "QSECOFR", password: "p\u00e4ssw\u00f6rd-\u00c4\u00d6\u00dc", want: "184a93917c1bd4cfbafae25edecfc5b5bc0e0f9900d41817213b3fffb0878154175b7efeb4463c1909be7f7ed758358a8dfb8b80856512cd5f1ba5ba011ca24d"},
	{level: 4, user: "QSECOFR", password: "MixedCase9", want: "6e8d809bfefba5936112c3d87a9164dcb0cd1eb3e415b16a6189db9c1791318fabeb95d0976ec5c1f99354ecb96d8bc21ec1286b1dd80d4276551b311cfe904c"},
	{level: 4, user: "QSECOFR", password: "a", want: "b57f585edd8f2068583fd9d765c70eba8c814aff56c9f641eaa1d191cbb6971af7dad5743a52a30418282238f015894c03535365fd81069de8d93648ab14ba6d"},
	{level: 4, user: "QSECOFR", password: "*star", wantErr: true},
	{level: 4, user: "QSECOFR", password: "", wantErr: true},
	{level: 2, user: "QSECOFR", password: "", wantErr: true},
	{level: 0, user: "QSECOFR", password: "", want: "868d90d6c1541032"},
	{level: 0, user: "QSECOFR", password: "TOOLONGPASSWORD", wantErr: true},
}

func TestPasswordSubstituteGoldenVectors(t *testing.T) {
	for _, tc := range passwordSubstituteGolden {
		got, err := passwordSubstitute(tc.user, tc.password, tc.level, goldenClientSeed, goldenServerSeed)
		if tc.wantErr {
			if err == nil {
				t.Errorf("level %d user %q password %q: expected error, got %x", tc.level, tc.user, tc.password, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("level %d user %q password %q: unexpected error: %v", tc.level, tc.user, tc.password, err)
			continue
		}
		if gotHex := hex.EncodeToString(got); gotHex != tc.want {
			t.Errorf("level %d user %q password %q: substitute mismatch\n got %s\nwant %s", tc.level, tc.user, tc.password, gotHex, tc.want)
		}
	}
}

func TestPasswordSubstituteLengths(t *testing.T) {
	cases := []struct {
		level int
		want  int
	}{{0, 8}, {1, 8}, {2, 20}, {3, 20}, {4, 64}, {5, 64}}
	for _, tc := range cases {
		got, err := passwordSubstitute("QSECOFR", "Secret1", tc.level, goldenClientSeed, goldenServerSeed)
		if err != nil {
			t.Fatalf("level %d: %v", tc.level, err)
		}
		if len(got) != tc.want {
			t.Errorf("level %d: substitute length %d, want %d", tc.level, len(got), tc.want)
		}
	}
}

func TestPasswordSubstituteSHATrimming(t *testing.T) {
	// JTOpen trims trailing NUL, space and U+3000 for levels 2-3 only.
	base, err := passwordSubstitute("QSECOFR", "Secret1", 2, goldenClientSeed, goldenServerSeed)
	if err != nil {
		t.Fatal(err)
	}
	trimmed, err := passwordSubstitute("QSECOFR", "Secret1   \x00　", 2, goldenClientSeed, goldenServerSeed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(base, trimmed) {
		t.Errorf("level 2 trailing blanks should be trimmed")
	}

	// Level 4 does not trim, so trailing blanks change the substitute.
	base4, err := passwordSubstitute("QSECOFR", "Secret1", 4, goldenClientSeed, goldenServerSeed)
	if err != nil {
		t.Fatal(err)
	}
	padded4, err := passwordSubstitute("QSECOFR", "Secret1   ", 4, goldenClientSeed, goldenServerSeed)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(base4, padded4) {
		t.Errorf("level 4 trailing blanks must not be trimmed")
	}
}

func TestPasswordAuthScheme(t *testing.T) {
	cases := map[int]byte{8: 0x01, 20: 0x03, 64: 0x07}
	for n, want := range cases {
		if got := passwordAuthScheme(n); got != want {
			t.Errorf("passwordAuthScheme(%d) = 0x%02x, want 0x%02x", n, got, want)
		}
	}
}

func TestDESEncryptReturnsErrorOnShortInput(t *testing.T) {
	if _, err := desEncrypt8([]byte{1, 2, 3}, make([]byte, 8)); err == nil {
		t.Error("desEncrypt8 with short key: expected error")
	}
	if _, err := desEncrypt8(make([]byte, 8), []byte{1}); err == nil {
		t.Error("desEncrypt8 with short data: expected error")
	}
	if _, err := generatePasswordSubstitute(make([]byte, 10), make([]byte, 4), make([]byte, 8), make([]byte, 8), make([]byte, 8), make([]byte, 8)); err == nil {
		t.Error("generatePasswordSubstitute with short token: expected error")
	}
}

func TestPasswordSubstituteRejectsBadSeeds(t *testing.T) {
	if _, err := passwordSubstitute("QSECOFR", "Secret1", 4, []byte{1, 2}, goldenServerSeed); err == nil {
		t.Error("expected error for short client seed")
	}
}
