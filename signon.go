package i400

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"strings"
	"time"
)

const (
	clientVersion         = 1
	clientDataStreamLevel = 2
	clientCCSID           = 1200
)

func connectSystemInfo(ctx context.Context, cfg Config, user, password string) (*SystemInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	signonPort := SignonPort
	if cfg.UseTLS {
		signonPort = SecureSignonPort
	}
	conn, err := dialHostServer(ctx, cfg, signonPort)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	setConnDeadline(conn, ctx)
	defer clearConnDeadline(conn)
	cancelDeadline := context.AfterFunc(ctx, func() {
		_ = conn.SetDeadline(time.Now())
	})
	defer cancelDeadline()

	info, clientSeed, serverSeed, err := signonExchange(conn, cfg.Host)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}

	if err := signonAuthenticate(conn, info, user, password, clientSeed, serverSeed); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}

	return info, nil
}

func signonExchange(conn net.Conn, system string) (*SystemInfo, []byte, []byte, error) {
	clientSeed := make([]byte, 8)
	binary.BigEndian.PutUint64(clientSeed, uint64(time.Now().UnixMilli()))

	request := buildHeader(52, 0, SignonServerID, 0, RequestExchangeAttributes)
	request = appendU32(request, 10)
	request = appendU16(request, CodePointClientVersion)
	request = appendU32(request, clientVersion)
	request = appendU32(request, 8)
	request = appendU16(request, CodePointClientDSLevel)
	request = appendU16(request, clientDataStreamLevel)
	request = appendU32(request, 14)
	request = appendU16(request, CodePointClientSeed)
	request = appendBytes(request, clientSeed)
	if _, err := conn.Write(request); err != nil {
		return nil, nil, nil, err
	}

	packet, err := readPacket(conn)
	if err != nil {
		return nil, nil, nil, err
	}
	rc, payload, err := readSimpleReplyForID(packet, ReplyExchangeAttributes)
	if err != nil {
		return nil, nil, nil, err
	}
	if rc != 0 {
		return nil, nil, nil, signonError(rc)
	}

	info := &SystemInfo{System: system}
	var serverSeed []byte
	for len(payload) >= 6 {
		cp, cpPayload, rest, err := ParseLLCP(payload)
		if err != nil {
			return nil, nil, nil, err
		}
		switch cp {
		case CodePointClientVersion:
			if len(cpPayload) >= 4 {
				info.ServerVersion = int(binary.BigEndian.Uint32(cpPayload[:4]))
			}
		case CodePointClientDSLevel:
			if len(cpPayload) >= 2 {
				info.ServerLevel = int(binary.BigEndian.Uint16(cpPayload[:2]))
			}
		case CodePointClientSeed:
			if len(cpPayload) >= 8 {
				serverSeed = make([]byte, 8)
				copy(serverSeed, cpPayload[:8])
			}
		case CodePointPasswordLevel:
			if len(cpPayload) >= 1 {
				info.PasswordLevel = int(cpPayload[0])
			}
		case CodePointJobName:
			if len(cpPayload) >= 4 {
				jobName, err := DecodeEBCDIC37(cpPayload[4:])
				if err == nil {
					info.JobName = jobName
				}
			}
		}
		payload = rest
	}
	if len(serverSeed) == 0 {
		return nil, nil, nil, fmt.Errorf("signon exchange reply missing server seed")
	}
	return info, clientSeed, serverSeed, nil
}

func signonAuthenticate(conn net.Conn, info *SystemInfo, user, password string, clientSeed, serverSeed []byte) error {
	userBytesForEncryption, err := signonUserBytes(user, info.PasswordLevel, true)
	if err != nil {
		return err
	}
	passwordBytes, err := signonPasswordBytes(password, info.PasswordLevel)
	if err != nil {
		return err
	}

	var encryptedPassword []byte
	if info.PasswordLevel >= 2 {
		encryptedPassword, err = encryptPasswordSHA(userBytesForEncryption, passwordBytes, clientSeed, serverSeed)
	} else {
		encryptedPassword, err = encryptPasswordDES(userBytesForEncryption, passwordBytes, clientSeed, serverSeed)
	}
	if err != nil {
		return err
	}

	wireUserBytes, err := signonUserBytes(user, 0, false)
	if err != nil {
		return err
	}

	length := 37 + len(encryptedPassword) + 16
	if info.ServerLevel >= 5 {
		length += 7
	}
	request := buildHeader(uint32(length), 0, SignonServerID, 1, RequestSignonInfo)
	authType := byte(3)
	if len(encryptedPassword) == 8 {
		authType = 1
	}
	request = append(request, authType)
	request = appendU32(request, 10)
	request = appendU16(request, CodePointClientCCSID)
	request = appendU32(request, clientCCSID)
	request = appendU32(request, uint32(6+len(encryptedPassword)))
	request = appendU16(request, CodePointPassword)
	request = appendBytes(request, encryptedPassword)
	request = appendU32(request, 16)
	request = appendU16(request, CodePointUserID)
	request = appendBytes(request, wireUserBytes)
	if info.ServerLevel >= 5 {
		request = appendU32(request, 7)
		request = appendU16(request, CodePointReturnErrorMessages)
		request = append(request, 1)
	}

	if _, err := conn.Write(request); err != nil {
		return err
	}

	packet, err := readPacket(conn)
	if err != nil {
		return err
	}
	rc, payload, err := readSimpleReplyForID(packet, ReplySignonInfo)
	if err != nil {
		return err
	}
	if rc != 0 {
		return signonError(rc)
	}
	for len(payload) >= 6 {
		cp, cpPayload, rest, err := ParseLLCP(payload)
		if err != nil {
			return err
		}
		if cp == CodePointServerCCSID && len(cpPayload) >= 4 {
			info.ServerCCSID = int(binary.BigEndian.Uint32(cpPayload[:4]))
		}
		payload = rest
	}
	return nil
}

func signonError(rc int32) error {
	switch rc {
	case 0x20001:
		return fmt.Errorf("%w: user ID is not known", ErrAuthentication)
	case 0x3000B:
		return fmt.Errorf("%w: password is incorrect", ErrAuthentication)
	case 0x3000C:
		return fmt.Errorf("%w: user profile will be revoked on next invalid password or passphrase", ErrAuthentication)
	case 0x3000D:
		return fmt.Errorf("%w: password is expired", ErrAuthentication)
	default:
		return fmt.Errorf("%w: signon failed with return code 0x%x", ErrAuthentication, uint32(rc))
	}
}

func signonUserBytes(user string, level int, forEncryption bool) ([]byte, error) {
	if level >= 2 && forEncryption {
		return EncodeUTF16BEBlankPad(strings.ToUpper(user), 20), nil
	}
	if len(user) > 10 {
		return nil, fmt.Errorf("user too long")
	}
	return EncodeEBCDIC37BlankPad(strings.ToUpper(user), 10)
}

func signonPasswordBytes(password string, level int) ([]byte, error) {
	if level >= 2 {
		return EncodeUTF16BEString(password), nil
	}
	if len(password) > 0 && password[0] >= '0' && password[0] <= '9' {
		password = "Q" + password
	}
	if len(password) > 10 {
		return nil, fmt.Errorf("password too long")
	}
	return EncodeEBCDIC37BlankPad(strings.ToUpper(password), 10)
}
