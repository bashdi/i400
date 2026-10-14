package i400

// vrmV5R3M0 is V5R3M0 in JTOpen's VRM encoding ((V<<16)|(R<<8)|M). Before it,
// JTOpen does not send UTF-16 (1200) statement text and uses 13488 instead.
const vrmV5R3M0 = 0x050300

// statementTextCCSIDFor resolves the CCSID tag used for SQL statement text,
// following JTOpen's package ccsid handling:
//   - "system" uses the server job CCSID from signon (EBCDIC 37 when unknown).
//   - 1200 is sent as 1200 unless the server is older than V5R3M0, then 13488.
//   - any other positive value is used as given (13488 by default).
//
// serverCCSID and serverVRM come from the signon exchange; zero means unknown.
func statementTextCCSIDFor(cfg Config, serverCCSID, serverVRM int) int {
	if cfg.PackageCCSIDSystem {
		if serverCCSID > 0 {
			return serverCCSID
		}
		return 37
	}
	ccsid := cfg.PackageCCSID
	if ccsid <= 0 {
		return defaultPackageCCSID
	}
	if ccsid == 1200 && serverVRM != 0 && serverVRM < vrmV5R3M0 {
		return defaultPackageCCSID
	}
	return ccsid
}

// statementTextCCSID returns the CCSID tag and encoding for statement text on
// this connection. It applies whether or not extended dynamic is enabled, as
// JTOpen's package CCSID converter does.
func (c *Conn) statementTextCCSID() int {
	if c == nil {
		return defaultPackageCCSID
	}
	serverCCSID, serverVRM := 0, 0
	if c.systemInfo != nil {
		serverCCSID = c.systemInfo.ServerCCSID
		serverVRM = c.systemInfo.ServerVersion
	}
	return statementTextCCSIDFor(c.cfg, serverCCSID, serverVRM)
}
