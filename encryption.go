package i400

import (
	"crypto/des"
	"crypto/pbkdf2"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	// pwdLevel4Iterations and pwdLevel4KeyLength are the PBKDF2 parameters for QPWDLVL 4
	// (JTOpen AS400ImplRemote.generatePwdTokenForPasswordLevel4).
	pwdLevel4Iterations = 10022
	pwdLevel4KeyLength  = 64
	// maxUserIDLength and maxPasswordLengthDES mirror the IBM i limits.
	maxUserIDLength       = 10
	maxPasswordLengthDES  = 10
	passwordSubstituteDES = 8
	passwordSubstituteSHA = 20
)

var shaSequence = [8]byte{0, 0, 0, 0, 0, 0, 0, 1}

// passwordSubstitute builds the password substitute sent to the host for the given
// password level. It mirrors AS400ImplRemote.getPassword in JTOpen:
//
//   - level 0-1: DES substitute (8 bytes)
//   - level 2-3: SHA-1 substitute (20 bytes), password trailing NUL/space/U+3000 trimmed
//   - level 4+:  PBKDF2-HMAC-SHA512 token and SHA-512 substitute (64 bytes), password not trimmed
func passwordSubstitute(user, password string, level int, clientSeed, serverSeed []byte) ([]byte, error) {
	if len(clientSeed) < 8 || len(serverSeed) < 8 {
		return nil, errors.New("client and server seeds must be at least 8 bytes")
	}
	user = strings.ToUpper(user)
	if utf8.RuneCountInString(user) > maxUserIDLength {
		return nil, fmt.Errorf("user ID too long")
	}
	switch {
	case level < 2:
		return desPasswordSubstitute(user, password, clientSeed, serverSeed)
	case level < 4:
		return sha1PasswordSubstitute(user, password, clientSeed, serverSeed)
	default:
		return sha512PasswordSubstitute(user, password, clientSeed, serverSeed)
	}
}

// passwordAuthScheme returns the authentication scheme byte that precedes the password
// substitute in the signon and start-server requests (JTOpen SignonInfoReq / AS400StrSvrDS).
func passwordAuthScheme(substituteLen int) byte {
	switch substituteLen {
	case passwordSubstituteDES:
		return 0x01
	case passwordSubstituteSHA:
		return 0x03
	default:
		return 0x07
	}
}

func checkPasswordPresent(password string) error {
	if password == "" {
		return errors.New("password is empty")
	}
	if password[0] == '*' {
		return errors.New("password begins with a '*' character")
	}
	return nil
}

func desPasswordSubstitute(user, password string, clientSeed, serverSeed []byte) ([]byte, error) {
	// A numeric-leading password gets a Q prefix (JTOpen behaviour for DES).
	if password != "" && password[0] >= '0' && password[0] <= '9' {
		password = "Q" + password
	}
	if utf8.RuneCountInString(password) > maxPasswordLengthDES {
		return nil, errors.New("password too long")
	}
	userBytes, err := EncodeEBCDIC37BlankPad(user, maxUserIDLength)
	if err != nil {
		return nil, err
	}
	passwordBytes, err := EncodeEBCDIC37BlankPad(strings.ToUpper(password), maxPasswordLengthDES)
	if err != nil {
		return nil, err
	}
	return encryptPasswordDES(userBytes, passwordBytes, clientSeed, serverSeed)
}

func sha1PasswordSubstitute(user, password string, clientSeed, serverSeed []byte) ([]byte, error) {
	if err := checkPasswordPresent(password); err != nil {
		return nil, err
	}
	// JTOpen trims trailing NUL, space and ideographic space before hashing (SHA-1 paths only).
	trimmed := strings.TrimRight(password, "\x00 　")
	return encryptPasswordSHA(userIDUTF16(user), EncodeUTF16BEString(trimmed), clientSeed, serverSeed)
}

func sha512PasswordSubstitute(user, password string, clientSeed, serverSeed []byte) ([]byte, error) {
	if err := checkPasswordPresent(password); err != nil {
		return nil, err
	}
	token, err := passwordLevel4Token(user, password)
	if err != nil {
		return nil, err
	}
	return sha512Substitute(token, serverSeed, clientSeed, userIDUTF16(user), shaSequence[:]), nil
}

// userIDUTF16 returns the 10-character blank-padded upper-case user ID as UTF-16BE (20 bytes).
func userIDUTF16(user string) []byte {
	return EncodeUTF16BEBlankPad(strings.ToUpper(user), 20)
}

// passwordLevel4Salt implements the salt steps of the pwdlvl 4 algorithm:
// SHA-256 over the 10-character UTF-16BE user ID followed by the last 4 UTF-16 code units
// of the password (blank-padded to 4), which gives a 28-byte input.
func passwordLevel4Salt(user, password string) []byte {
	input := make([]byte, 0, 28)
	input = append(input, EncodeUTF16BEBlankPad(user, 20)...)
	units := utf16.Encode([]rune(password))
	start := len(units) - 4
	if start < 0 {
		start = 0
	}
	tail := units[start:]
	for i := 0; i < 4; i++ {
		unit := uint16(' ')
		if i < len(tail) {
			unit = tail[i]
		}
		input = append(input, byte(unit>>8), byte(unit))
	}
	sum := sha256.Sum256(input)
	return sum[:]
}

// passwordLevel4Token derives the 64-byte PW_TOKEN with PBKDF2-HMAC-SHA512.
// JTOpen passes the password as a char[] to PBEKeySpec, which the JDK encodes as UTF-8,
// so the Go password string is used directly.
func passwordLevel4Token(user, password string) ([]byte, error) {
	salt := passwordLevel4Salt(strings.ToUpper(user), password)
	token, err := pbkdf2.Key(sha512.New, password, salt, pwdLevel4Iterations, pwdLevel4KeyLength)
	if err != nil {
		return nil, fmt.Errorf("pbkdf2 failed: %w", err)
	}
	return token, nil
}

// sha512Substitute computes PW_SUB = SHA-512(PW_TOKEN, RDr, RDs, ID, PWSEQs).
func sha512Substitute(token, serverSeed, clientSeed, userIDBytes, sequence []byte) []byte {
	h := sha512.New()
	_, _ = h.Write(token)
	_, _ = h.Write(serverSeed[:8])
	_, _ = h.Write(clientSeed[:8])
	_, _ = h.Write(userIDBytes)
	_, _ = h.Write(sequence)
	return h.Sum(nil)
}

func encryptPasswordSHA(userID, password, clientSeed, serverSeed []byte) ([]byte, error) {
	if len(userID) == 0 || len(clientSeed) < 8 || len(serverSeed) < 8 {
		return nil, fmt.Errorf("invalid SHA password inputs")
	}
	hash := sha1.New()
	_, _ = hash.Write(userID)
	_, _ = hash.Write(password)
	token := hash.Sum(nil)
	if len(token) != sha1.Size {
		return nil, errors.New("unexpected SHA token length")
	}
	hash.Reset()
	_, _ = hash.Write(token)
	_, _ = hash.Write(serverSeed[:8])
	_, _ = hash.Write(clientSeed[:8])
	_, _ = hash.Write(userID)
	_, _ = hash.Write(shaSequence[:])
	return hash.Sum(nil), nil
}

func encryptPasswordDES(userID, password, clientSeed, serverSeed []byte) ([]byte, error) {
	token, err := generateToken(userID, password)
	if err != nil {
		return nil, err
	}
	sequenceNumber := [8]byte{0, 0, 0, 0, 0, 0, 0, 1}
	verifyToken := make([]byte, 8)
	return generatePasswordSubstitute(userID, token, verifyToken, sequenceNumber[:], clientSeed, serverSeed)
}

func generateToken(userID, password []byte) ([]byte, error) {
	if len(userID) < maxUserIDLength {
		return nil, errors.New("invalid DES user ID length")
	}
	token := make([]byte, 8)
	workBuffer1 := make([]byte, 10)
	workBuffer2 := []byte{0x40, 0x40, 0x40, 0x40, 0x40, 0x40, 0x40, 0x40, 0x40, 0x40}
	workBuffer3 := []byte{0x40, 0x40, 0x40, 0x40, 0x40, 0x40, 0x40, 0x40, 0x40, 0x40}
	copy(workBuffer1, userID)

	if ebcdicStrLen(userID, 10) > 8 {
		workBuffer1[0] ^= workBuffer1[8] & 0xC0
		workBuffer1[1] ^= (workBuffer1[8] & 0x30) << 2
		workBuffer1[2] ^= (workBuffer1[8] & 0x0C) << 4
		workBuffer1[3] ^= (workBuffer1[8] & 0x03) << 6
		workBuffer1[4] ^= workBuffer1[9] & 0xC0
		workBuffer1[5] ^= (workBuffer1[9] & 0x30) << 2
		workBuffer1[6] ^= (workBuffer1[9] & 0x0C) << 4
		workBuffer1[7] ^= (workBuffer1[9] & 0x03) << 6
	}

	length := ebcdicStrLen(password, 10)
	if length > 8 {
		copy(workBuffer2, password[:8])
		copy(workBuffer3, password[8:length])
		xorWith0x55AndLshift(workBuffer2)
		enc1, err := desEncrypt8(workBuffer2, workBuffer1[:8])
		if err != nil {
			return nil, err
		}
		xorWith0x55AndLshift(workBuffer3)
		enc2, err := desEncrypt8(workBuffer3, workBuffer1[:8])
		if err != nil {
			return nil, err
		}
		xorBytes(enc1, enc2, token)
	} else {
		copy(workBuffer2, password[:length])
		xorWith0x55AndLshift(workBuffer2)
		enc, err := desEncrypt8(workBuffer2, workBuffer1[:8])
		if err != nil {
			return nil, err
		}
		copy(token, enc)
	}
	return token, nil
}

func generatePasswordSubstitute(userID, token, passwordVerifier, sequenceNumber, clientSeed, serverSeed []byte) ([]byte, error) {
	if len(userID) < maxUserIDLength || len(token) < 8 || len(passwordVerifier) < 8 || len(sequenceNumber) < 8 || len(clientSeed) < 8 || len(serverSeed) < 8 {
		return nil, fmt.Errorf("invalid DES password inputs")
	}
	var err error
	var rDrSEQ, nextData, nextEncryptedData []byte
	rDrSEQ = make([]byte, 8)
	nextData = make([]byte, 8)

	addBytes(sequenceNumber[:8], serverSeed[:8], rDrSEQ)
	if nextEncryptedData, err = desEncrypt8(token[:8], rDrSEQ); err != nil {
		return nil, err
	}
	xorBytes(nextEncryptedData, clientSeed[:8], nextData)
	if nextEncryptedData, err = desEncrypt8(token[:8], nextData); err != nil {
		return nil, err
	}
	copy(passwordVerifier, nextEncryptedData[:8])
	xorBytes(userID[:8], rDrSEQ, nextData)
	xorBytes(nextData, nextEncryptedData, nextData)
	if nextEncryptedData, err = desEncrypt8(token[:8], nextData); err != nil {
		return nil, err
	}
	for i := 0; i < 8; i++ {
		nextData[i] = 0x40
	}
	nextData[0] = userID[8]
	nextData[1] = userID[9]
	xorBytes(rDrSEQ, nextData, nextData)
	xorBytes(nextData, nextEncryptedData, nextData)
	if nextEncryptedData, err = desEncrypt8(token[:8], nextData); err != nil {
		return nil, err
	}
	xorBytes(sequenceNumber[:8], nextEncryptedData, nextData)
	return desEncrypt8(token[:8], nextData)
}

func desEncrypt8(key, data []byte) ([]byte, error) {
	if len(key) < 8 || len(data) < 8 {
		return nil, errors.New("DES key and data must be at least 8 bytes")
	}
	block, err := des.NewCipher(key[:8])
	if err != nil {
		return nil, fmt.Errorf("DES key: %w", err)
	}
	out := make([]byte, 8)
	block.Encrypt(out, data[:8])
	return out, nil
}

func xorBytes(left, right, out []byte) {
	for i := 0; i < len(out) && i < len(left) && i < len(right); i++ {
		out[i] = left[i] ^ right[i]
	}
}

func addBytes(left, right, out []byte) {
	carry := 0
	for i := len(out) - 1; i >= 0; i-- {
		sum := int(left[i]&0xff) + int(right[i]&0xff) + carry
		carry = sum >> 8
		out[i] = byte(sum)
	}
}

func xorWith0x55AndLshift(bytes []byte) {
	for i := 0; i < 8 && i < len(bytes); i++ {
		bytes[i] ^= 0x55
	}
	for i := 0; i < 7 && i < len(bytes); i++ {
		bytes[i] = byte(bytes[i]<<1 | (bytes[i+1]&0x80)>>7)
	}
	if len(bytes) > 0 {
		bytes[7] <<= 1
	}
}

func ebcdicStrLen(data []byte, maxLength int) int {
	i := 0
	for i < maxLength && i < len(data) && data[i] != 0x40 && data[i] != 0 {
		i++
	}
	return i
}
