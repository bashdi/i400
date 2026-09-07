package i400

import (
	"crypto/des"
	"crypto/sha1"
	"errors"
	"fmt"
)

var shaSequence = [8]byte{0, 0, 0, 0, 0, 0, 0, 1}

func encryptPasswordSHA(userID, password, clientSeed, serverSeed []byte) ([]byte, error) {
	if len(userID) == 0 || len(password) == 0 || len(clientSeed) < 8 || len(serverSeed) < 8 {
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
	token := generateToken(userID, password)
	sequenceNumber := [8]byte{0, 0, 0, 0, 0, 0, 0, 1}
	verifyToken := make([]byte, 8)
	return generatePasswordSubstitute(userID, token, verifyToken, sequenceNumber[:], clientSeed, serverSeed)
}

func generateToken(userID, password []byte) []byte {
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
		enc1 := desEncrypt8(workBuffer2, workBuffer1[:8])
		xorWith0x55AndLshift(workBuffer3)
		enc2 := desEncrypt8(workBuffer3, workBuffer1[:8])
		xorBytes(enc1, enc2, token)
	} else {
		copy(workBuffer2, password[:length])
		xorWith0x55AndLshift(workBuffer2)
		copy(token, desEncrypt8(workBuffer2, workBuffer1[:8]))
	}
	return token
}

func generatePasswordSubstitute(userID, token, passwordVerifier, sequenceNumber, clientSeed, serverSeed []byte) ([]byte, error) {
	if len(token) < 8 || len(passwordVerifier) < 8 || len(sequenceNumber) < 8 || len(clientSeed) < 8 || len(serverSeed) < 8 {
		return nil, fmt.Errorf("invalid DES password inputs")
	}
	rDrSEQ := make([]byte, 8)
	nextData := make([]byte, 8)
	nextEncryptedData := make([]byte, 8)
	addBytes(sequenceNumber[:8], serverSeed[:8], rDrSEQ)
	nextEncryptedData = desEncrypt8(token[:8], rDrSEQ)
	xorBytes(nextEncryptedData, clientSeed[:8], nextData)
	nextEncryptedData = desEncrypt8(token[:8], nextData)
	copy(passwordVerifier, nextEncryptedData[:8])
	xorBytes(userID[:8], rDrSEQ, nextData)
	xorBytes(nextData, nextEncryptedData, nextData)
	nextEncryptedData = desEncrypt8(token[:8], nextData)
	for i := 0; i < 8; i++ {
		nextData[i] = 0x40
	}
	if len(userID) > 8 {
		nextData[0] = userID[8]
	}
	if len(userID) > 9 {
		nextData[1] = userID[9]
	}
	xorBytes(rDrSEQ, nextData, nextData)
	xorBytes(nextData, nextEncryptedData, nextData)
	nextEncryptedData = desEncrypt8(token[:8], nextData)
	xorBytes(sequenceNumber[:8], nextEncryptedData, nextData)
	return desEncrypt8(token[:8], nextData), nil
}

func desEncrypt8(key, data []byte) []byte {
	block, err := des.NewCipher(key[:8])
	if err != nil {
		panic(err)
	}
	out := make([]byte, 8)
	block.Encrypt(out, data[:8])
	return out
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
