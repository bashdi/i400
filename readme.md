# Usage

```go
package main

import (
	"database/sql"
	"log"

	_ "github.com/bashdi/i400"
)

func main() {
	db, err := sql.Open("i400", "as400://USER:PASSWORD@ibmi.example.com:8471/MYLIB")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatal(err)
	}
}
```

`as400s://` can be used for a TLS connection.

## DSN format

The DSN has the following format:

```text
SCHEME://USER:PASSWORD@HOST[:PORT][/LIBRARY][?OPTION=VALUE&...]
```

Required:

- `SCHEME`: `as400` for an unencrypted connection or `as400s` for TLS.
- `HOST`: Hostname or IP address of the IBM i system.

Optional:

- `USER:PASSWORD`: Credentials. They are optional during DSN parsing, but the server must authenticate the connection successfully.
- `PORT`: IBM i database service port. Defaults to `8471` for `as400` and `9471` for `as400s`. The `port` and `portNumber` options are also accepted (see the connection options).
- `/LIBRARY`: Default schema (library). If omitted, no schema is configured.
- `?OPTION=VALUE`: Optional driver settings described below.

Usernames, passwords, and library names must be URL-encoded when they contain characters such as `@`, `:`, or spaces.

### DSN options

All options are optional. Boolean values are `true` or `false` (as parsed by `strconv.ParseBool`, so `1`/`0` also work). Invalid values are rejected by `ParseDSN` with `ErrInvalidDSN`.

Options marked with a JTOpen spelling in the tables (for example `query optimize goal`) are read through a lookup that ignores case, spaces, underscores, and dashes, so `queryOptimizeGoal`, `query optimize goal`, and `query_optimize_goal` are equivalent. Options written as a single key only (`tls`, `secure`, `naming`, `dateFormat`, `dateSeparator`, `timeFormat`, `timeSeparator`, `decimalSeparator`, `commitmentControl`, `commitControl`, `libraries`, `statementCacheSize`, `scrollableCursors`, `holdCursors`, `bidiImplicitReordering`, `bidiNumericOrdering`, `connectTimeout`, `timeout`, `port`, `portNumber`) accept only the key shown.

#### Connection and socket

| Option | Values / format | Default | Description |
| --- | --- | --- | --- |
| `tls` / `secure` | `true`, `false` | Based on the scheme | Enables or disables TLS. Either option overrides the scheme. |
| `port` / `portNumber` | Integer | `8471` (`as400`), `9471` (`as400s`) | Service port. `port` takes precedence over `portNumber`, which takes precedence over the port in the URL. |
| `naming` | `sql`, `system` | `sql` | SQL naming or IBM i system naming. |
| `libraries` | Comma-separated library names | Empty | Library list set on the connection. |
| `connectTimeout` / `timeout` | Go duration such as `5s`, or a whole number of seconds | `15s` | Maximum time allowed to open the connection. `connectTimeout` takes precedence. |
| `keepAlive` / `keep alive` | `true`, `false` | Platform default | Enables or disables TCP keep-alive probes. |
| `tcpNoDelay` / `tcp no delay` | `true`, `false` | Go default (`true`) | Sets `TCP_NODELAY`. `true` disables Nagle's algorithm. |
| `receiveBufferSize` / `receive buffer size` | Integer bytes, `0`-`16777216` | OS default | Socket receive buffer size. `0` keeps the operating system default. |
| `sendBufferSize` / `send buffer size` | Integer bytes, `0`-`16777216` | OS default | Socket send buffer size. `0` keeps the operating system default. |
| `socketTimeout` / `socket timeout` | Bare integer (milliseconds, as in JTOpen) or Go duration such as `30s` | `0` (no timeout) | Maximum wait for each read from the server, like JTOpen's `SO_TIMEOUT`. An earlier context deadline still applies. |

#### Session and formats

| Option | Values / format | Default | Description |
| --- | --- | --- | --- |
| `commitmentControl` / `commitControl` | `none`, `cs`, `chg`, `all`, `rr`, or `0`-`4` | `cs` | Commitment-control level: none, cursor stability, changed page, all, or repeatable read. `commitControl` is read after `commitmentControl` and overrides it. |
| `trueAutocommit` / `true autocommit` | `true`, `false` | `false` | With autocommit on, keeps the configured `commitmentControl` level instead of running under `*NONE`. |
| `queryOptimizeGoal` / `query optimize goal` | `0` or `default`, `1` or `firstio`, `2` or `allio` | `0` (server default) | Query optimization goal. Sent to the server only when not `0`. |
| `dateFormat` | `julian`, `mdy`, `dmy`, `ymd`, `usa`, `iso`, `eur`, `jis`, or `0`-`7` | `iso` | Date format used by the server. |
| `dateSeparator` | `slash`, `dash`, `dot`, `period`, `comma`, `space`, or `0`-`4` | `slash` | Date separator used by the server. |
| `timeFormat` | `hms`, `usa`, `iso`, `eur`, `jis`, or `0`-`4` | `hms` | Time format used by the server. |
| `timeSeparator` | `colon`, `dot`, `period`, `comma`, `space`, or `0`-`3` | `colon` | Time separator used by the server. |
| `decimalSeparator` | `dot`, `period`, `comma`, `0`, or `1` | `dot` | Decimal separator used by the server. |
| `decimalMaxPrecision` / `decimal max precision` | `31` or `63` | `31` | Maximum DECIMAL precision. A `decimalMaxScale` above 31 forces precision 63, as in JTOpen. |
| `decimalMaxScale` / `decimal max scale` | Integer `0`-`63` | `31` | Maximum DECIMAL scale. |
| `minimumDivideScale` / `minimum divide scale` | Integer `0`-`9` | `0` | Minimum scale of the result of a decimal division. |
| `decfloatRoundingMode` / `decfloat rounding mode` | `half-even`, `half-up`, `down`, `ceiling`, `floor`, `half-down`, `up`, or `0`-`6` | `half-even` | Rounding mode for DECFLOAT arithmetic on the server. |
| `errors` | `basic`, `full` | `basic` | SQL error text detail. `basic` requests the message ID and first-level text. `full` also requests the second-level text. See Notes. |
| `bidiStringType` / `bidi string type` | `0`, `4` to `11`, or `-1` (`none`, or an empty value: not set) | `5` | Bidi layout of character data in the CCSIDs that JTOpen converts with a bidi table (for example 420, 424, 862, 916, 1255, 1256). The default is `5` as in JTOpen. Not set uses each column's CCSID string type, as JTOpen's JDBC driver does. The same setting drives the layout of SQL statement text and column names. See Known Gaps. |
| `bidiImplicitReordering` | `true`, `false` | `true` | Implicit to implicit reordering. Off turns it off for character data, SQL statement text and column names. |
| `bidiNumericOrdering` | `true`, `false` | `false` | Numeric ordering round trip for character data in the bidi CCSIDs. |

#### Performance, cursors and LOBs

| Option | Values / format | Default | Description |
| --- | --- | --- | --- |
| `blockSize` / `block size` | KB: `0`, `8`, `16`, `32`, `64`, `128`, `256`, `512`, or `513`-`16000` | `256` | Fetch block size in KB. `0` uses the driver default of 256 KB. |
| `lobThreshold` / `lob threshold` | Integer bytes, `0`-`15728640` | `1048576` | Inline LOB threshold in bytes. Values above 15728640 are rejected. |
| `statementCacheSize` | Non-negative integer | `0` | Number of prepared statements cached per connection. `0` disables statement caching. Cached statements are never shared across connections. |
| `scrollableCursors` | `true`, `false` | `false` | Enables the driver's scrollable cursor extension (`ScrollableRows`). |
| `timestampPrecision` | `nanoseconds`, `string` | `nanoseconds` | Result type of TIMESTAMP columns. `nanoseconds` decodes to `time.Time`, which drops fractions finer than a nanosecond. `string` returns the IBM i text with every fractional digit the server sent; scan it into `i400.Timestamp` (picoseconds) or a `string`. Only result columns are affected. |
| `holdCursors` | `true`, `false` | `false` | Requests cursors that stay open across commits, where the server supports it. |
| `useBlockUpdate` / `use block update` | `true`, `false` | `false` | JTOpen "use block update". When `true`, `Conn.ExecBatch` and `Stmt.ExecBatch` send eligible rows as parameter blocks. See "Batch execution" below. |
| `queryStorageLimit` / `query storage limit` | Integer bytes; `-1` (`*NOMAX`) or a positive value | `-1` | Query storage limit (set-attributes `0x3834`, V6R1 and later). `-1` is not sent. Values below `-1` act as `-1`; values above 2147352578 act as 2147352578. |
| `queryTimeoutMechanism` / `query timeout mechanism` | `cancel`, `qqrytimlmt` | `cancel` | How a context deadline is enforced. `cancel` uses only the driver's client-side cancel. `qqrytimlmt` also sends the remaining time to the server (see below). |

#### Errors and generated keys

| Option | Values / format | Default | Description |
| --- | --- | --- | --- |
| `returnGeneratedKeys` / `return generated keys` | `true`, `false` | `false` | When `true`, an `INSERT` run through `Exec`/`ExecContext` is wrapped as `SELECT *SQLGENCOLUMNS FROM NEW TABLE(<insert>)` (V6R1+, as in JTOpen) so `LastInsertId` returns the generated key and `RowsAffected` counts the returned rows. This changes the SQL that runs. Non-INSERT statements are not changed. See Notes. |

#### Extended dynamic packages

| Option | Values / format | Default | Description |
| --- | --- | --- | --- |
| `extendedDynamic` / `extended dynamic` | `true`, `false` | `false` | Stores prepared statements in an IBM i SQL package, as in JTOpen. |
| `package` | Package name | Empty | Package name. Only the first six characters are used, upper-cased, with blanks replaced by underscores; a four-character suffix derived from the connection settings is appended, as in JTOpen. Required when `extendedDynamic=true`. |
| `packageLibrary` / `package library` | Library name | `QGPL` | Library that holds the package. Upper-cased. |
| `packageCriteria` / `package criteria` | `default`, `select` | `default` | Which statements are packaged. `default` packages statements with parameter markers, `INSERT ... SELECT`, `SELECT ... FOR UPDATE`, and `DECLARE`. `select` also packages every `SELECT`, `WITH`, and `VALUES`. |
| `packageAdd` / `package add` | `true`, `false` | `true` | Allows the server to add statements to the package. Sent in the connect-time server attributes on every connection, as JTOpen does. |
| `packageError` / `package error` | `none`, `warning`, `exception` | `warning` | What happens when the package cannot be created or used. `warning` records an SQL warning and continues without packaging, `exception` fails the statement, `none` continues silently. |
| `packageCCSID` / `package ccsid` | Positive CCSID, or `system` | `13488` | CCSID used to encode SQL statement text sent to the server, and the tag sent with it. `13488` and `1200` send UTF-16BE with that tag (`1200` falls back to `13488` before V5R3). `system` encodes with the server job CCSID from signon (`37` if unknown). Other CCSIDs encode the text with their conversion table. As in JTOpen, this applies with or without extended dynamic. |
| `packageCache` / `package cache` | `true`, `false` | `false` | Client-side package cache. Only applies with `extendedDynamic=true`. When the package already exists, its statements are downloaded (RETURN_PACKAGE) and a packaged statement whose text matches a cached statement is bound to the cached package statement without a prepare, as in JTOpen. Statements whose parameter formats contain LOBs are always prepared again. |
| `packageClear` / `package clear` | `true`, `false` | `false` | Parsed for JTOpen compatibility and has no effect. JTOpen no longer sends the clearing threshold; the server clears full packages itself. |

#### Client info

These values are sent to the server on connect when set (V6R1 and later). They can also be changed at runtime with `Conn.SetClientInfo`.

| Option | Values / format | Default | Description |
| --- | --- | --- | --- |
| `applicationName` / `application name` | Text, up to 255 bytes | Empty | Client info `ApplicationName`. |
| `clientUser` / `client user` | Text, up to 255 bytes | Empty | Client info `ClientUser`. |
| `clientAccounting` / `client accounting` | Text, up to 255 bytes | Empty | Client info `ClientAccounting`. |
| `clientHostname` / `client hostname` | Text, up to 255 bytes | Empty | Client info `ClientHostname`. |
| `clientProgramID` / `client program id` | Text, up to 255 bytes | Empty | Client info `ClientProgramID`. |

For example:

```text
as400://USER:PASSWORD@ibmi.example.com/MYLIB?naming=sql&libraries=APPDATA,APPLIB&connectTimeout=10s&statementCacheSize=20
```

### Batch execution

`Conn.ExecBatch` and `Stmt.ExecBatch` run one statement for many parameter rows and return the total rows affected. The statement is prepared once. This is a driver-level extension, reached through `sql.Conn.Raw`:

```go
conn, err := db.Conn(ctx)
if err != nil {
	return err
}
defer conn.Close()

var affected int64
err = conn.Raw(func(driverConn any) error {
	c := driverConn.(*i400.Conn)
	n, err := c.ExecBatch(ctx, "INSERT INTO MYLIB.ORDERS VALUES(?, ?, ?)", [][]driver.Value{
		{int64(1), "widget", 3.5},
		{int64(2), "gadget", nil},
		{int64(3), "gizmo", 7.25},
	})
	affected = n
	return err
})
```

Without `useBlockUpdate=true`, each row is one EXECUTE on the prepared statement. With `useBlockUpdate=true`, eligible rows go to the server in blocks, as JTOpen does with "use block update". Eligible statements are `INSERT ... VALUES(?, ...)` with only parameter markers in the VALUES clause, and `UPDATE` or `DELETE`. JTOpen blocks `UPDATE` and `DELETE` only on V7R1 and later; on older or unknown servers they run row by row. Statements with LOB parameters are never blocked. Each block is one EXECUTE that carries the row count, the indicators (`0xFFFF` for NULL) and the row data. Consecutive rows with the same encoded shape share a block. A block holds at most 32000 rows and about 1 MiB of parameter data; a single larger row is still sent alone. The update count is the sum of the block counts. If a block fails, the error is returned with the count from the blocks that succeeded.

Rows are positional: row values bind to the placeholders in the order they appear in the query, including named placeholders. Values are normalized the same way as normal parameters. `sql.Out` is rejected.

### Client info at runtime (`SetClientInfo`)

`Conn.SetClientInfo(ctx, name, value)` sets one JTOpen client info value on the server. Reach it through `sql.Conn.Raw`:

```go
err := conn.Raw(func(driverConn any) error {
	c := driverConn.(*i400.Conn)
	return c.SetClientInfo(ctx, "ClientUser", "ALICE")
})
```

Names are case-sensitive, as in JTOpen: `ApplicationName`, `ClientUser`, `ClientAccounting`, `ClientHostname`, `ClientProgramID`. An unknown name returns `ErrUnknownClientInfo`. `Conn.ClientInfo(name)` returns the current value, or `""` for an unknown name. Below V6R1 the value is kept on the client only, as in JTOpen. `ResetSession` (used when a pooled connection is reused) sends the DSN values back to the server, so a runtime change does not leak to the next user.

Differences from JTOpen:

- JTOpen's `queryTimeoutMechanism` defaults to `qqrytimlmt`. Here the default is `cancel`, so existing behaviour is unchanged. Set `queryTimeoutMechanism=qqrytimlmt` to get the server-side limit. With `qqrytimlmt`, the remaining time (whole seconds, at least 1) is sent in the statement's RPB (`0x3817`) before each open or execute. A deadline is needed to send it, and it costs one extra request per execution.
- JTOpen does not send the DSN client info values on connect; only its `setClientInfo` does. This driver also sends the non-empty DSN values in the connect-time set-attributes request (V6R1 and later).
- Values are encoded as CCSID 37 (EBCDIC), like the driver's other text attributes, not the connection CCSID. A value longer than 255 bytes is an error.

## Running integration tests

The `integration_*_test.go` files and `database_sql_crud_test.go` run against a live IBM i system. They are skipped unless the environment is configured, and they are skipped with `go test -short`, so `go test ./...` works without a system.

The simplest setup is a `.env` file in the repository root (it is listed in `.gitignore`). Copy `.env_template` to `.env` and fill it in:

```text
I400_ADRESSE=pub400.com
I400_USER=MYUSER
I400_PASSWORT=secret
I400_BIBLIOTHEK=MYUSER1
```

The tests build the DSN `as400://I400_USER:I400_PASSWORT@I400_ADRESSE` from these values and use `I400_BIBLIOTHEK` as the library for test objects. Variables already set in the environment take precedence over `.env`.

Alternatively, set these environment variables:

- `I400_TEST_DSN` (required): DSN for the test system, for example `as400://USER:PASSWORD@ibmi.example.com/?naming=sql`. Keep it out of source control.
- `I400_TEST_LIBRARY` (required by tests that create tables, procedures or packages): an existing library where the tests create uniquely named objects, for example `ITTEST`. Each test drops what it creates at cleanup. The user needs authority to create and drop objects there. Savepoint tests run commitment control, so the library should hold journaled tables.

PowerShell:

```powershell
$env:I400_TEST_DSN = "as400://USER:PASSWORD@ibmi.example.com/?naming=sql"
$env:I400_TEST_LIBRARY = "ITTEST"
go test -count=1 -v -run 'Integration|DatabaseSQLCRUD' ./...
Remove-Item Env:I400_TEST_DSN, Env:I400_TEST_LIBRARY
```

bash:

```bash
export I400_TEST_DSN='as400://USER:PASSWORD@ibmi.example.com/?naming=sql'
export I400_TEST_LIBRARY=ITTEST
go test -count=1 -v -run 'Integration|DatabaseSQLCRUD' ./...
unset I400_TEST_DSN I400_TEST_LIBRARY
```

Without `I400_TEST_LIBRARY`, the tests that need a library skip and the others still run. Run a single test with `-run TestIntegrationCCSIDRoundTrip`.

# i400 Capability Matrix

## database/sql Surface

| Area | Status | Notes |
| --- | --- | --- |
| `sql.Open` / `sql.OpenDB` / `Connector` / `Conn` | Supported | Connection lifecycle is wired through the IBM i host-server protocols. `driver.DriverContext` and `driver.Connector` are implemented. |
| `PingContext` | Supported | Uses a lightweight test-connection request. |
| `ResetSession` | Supported | Restores autocommit, commitment control, warnings, the configured library list, and the DSN client info values. |
| `BeginTx` / `Commit` / `Rollback` | Supported | Transaction state maps to auto-commit and commitment-control requests. |
| Savepoints | Supported | `SAVEPOINT`, `ROLLBACK TO SAVEPOINT`, and `RELEASE SAVEPOINT` are executed via `tx.Exec()` using IBM i DB2 SQL syntax. |
| `ExecContext` / `QueryContext` without args | Supported | Direct SQL text execution for all common statement classes. |
| `ExecContext` / `QueryContext` with positional args (`?`) | Supported | Parameters bound through the server-side descriptor path. |
| `ExecContext` / `QueryContext` with named args (`:name`) | Supported | Query rewritten to positional `?` markers before prepare; args reordered by name. |
| Prepared statements (`Prepare` / `PrepareContext`) | Supported | Positional and named placeholders; descriptor returned by server. |
| `sql.Out` (output / input-output parameters) | Supported | Callable-statement output parameters are assigned back into destinations. |
| `Rows.NextResultSet` | Supported | Multi-result-set cursors handled through cursor reuse and reopen. |
| `RowsColumnType*` metadata | Supported | Database type name, scan type, nullability, LOB length, and precision/scale (including DECFLOAT) are exposed when supplied by the server. |
| Statement caching | Opt-in | Reuses prepared statements per connection when `statementCacheSize` is greater than zero; active-use tracking, bounded FIFO eviction, and cleanup on connection close are applied. The default is uncached. |
| Extended dynamic packages | Opt-in | Enable with `extendedDynamic=true` and `package`. Statements are stored in an IBM i SQL package. Optional client-side package cache with `packageCache=true`. See Known Gaps. |
| Scrollable cursors | Opt-in extension | Enable with `scrollableCursors=true`; driver-level `ScrollableRows` supports `BeforeFirst`, `AfterLast`, `Current`, `First`, `Last`, `Absolute`, `Relative`, and `Row` (zero when no current row is known). Standard `database/sql.Rows` exposes only `Next`. |
| Holdable cursors | Opt-in | Enable with `holdCursors=true`; open/describe requests send IBM i hold indicators so cursors survive commit where supported by the server. |
| Batch execution (`ExecBatch`) | Opt-in extension | `Conn.ExecBatch` and `Stmt.ExecBatch`, reached through `sql.Conn.Raw`. With `useBlockUpdate=true`, eligible rows are sent as parameter blocks. |
| Generated keys | Supported | `LastInsertId` reads the key the server returns in the SQLCA. `returnGeneratedKeys=true` adds the `SQLGENCOLUMNS` read for INSERT statements. |
| Client info | Supported | DSN values are sent on connect; `SetClientInfo` changes a value at runtime. V6R1 and later. |
| `Stmt.Close` | Supported | Prepared statements are closed explicitly when possible. |
| `Conn.Close` | Supported | Sends an end-job request before closing the socket. |
| Context cancellation | Supported | Protocol-native cancel when server metadata is available; deadline-based fallback otherwise. |
| Connection pooling hints | Supported | `IsValid` and `ResetSession` implemented for pool reuse. |
| `NamedValueChecker` | Supported | Normalizes `int`/`uint`/`float32`, `time.Time`, and `driver.Valuer` types automatically. |

## Type Mapping

### Result-set columns (server → Go)

| DB2 / IBM i Type | Go type returned |
| --- | --- |
| `DATE` | `time.Time` (UTC), parsed with the connection's `dateFormat` and `dateSeparator` |
| `TIME` | `time.Time` (UTC, year 0, January 1), parsed with `timeFormat` and `timeSeparator`. A blank value is returned as an empty string. |
| `TIMESTAMP` | With `timestampPrecision=nanoseconds` (default): `time.Time` (UTC) from `YYYY-MM-DD-HH.MM.SS[.fraction]`. Fractions longer than nanoseconds are truncated, not rounded. With `timestampPrecision=string`: the IBM i text as sent, all digits kept. Scan it into `i400.Timestamp` for picoseconds, or into `i400.NullTimestamp` for NULL-able columns. Text that does not parse is returned as a string. |
| `CHAR`, `VARCHAR`, `LONG VARCHAR`, `GRAPHIC`, `VARGRAPHIC`, `LONG VARGRAPHIC`, `DATALINK` | `string` (decoded from the column CCSID) |
| `DECIMAL`, `NUMERIC` | `string` (exact digits) |
| `FLOAT` (4 / 8 bytes) | `float64` |
| `BIGINT`, `INTEGER`, `SMALLINT` | `int64` |
| `DECFLOAT(16)`, `DECFLOAT(34)` | `string` (decimal text decoded from IEEE 754 DPD) |
| `BOOLEAN` | `bool` (`0xF0` is false, any other byte is true) |
| `BINARY`, `VARBINARY`, `ROWID` | `[]byte` |
| `CLOB`, `DBCLOB`, `XML` | `string` decoded from the column CCSID. CCSID 65535 returns `[]byte`. A leading BOM is removed from XML text. |
| `BLOB` | `[]byte` |

LOB locator columns (`BLOB`, `CLOB`, `DBCLOB`, `XML` locators) are resolved automatically and decoded the same way as the LOB types above.

### Parameter inputs (Go → server)

| Go type | Accepted as |
| --- | --- |
| `nil` | `NULL` |
| `int64`, `int`, `int8`, `int16`, `int32` | integer; out-of-range values for the column type are rejected |
| `uint`, `uint8`, `uint16`, `uint32`, `uint64` | integer (converted to `int64` when within range; overflow is rejected) |
| `float64`, `float32` | floating-point (`float32` is converted to `float64`). For DECFLOAT columns the value is formatted as decimal text. |
| `bool` | `BOOLEAN` columns: one byte, `0xF1` for true and `0xF0` for false. Numeric columns: `1` or `0`. |
| `string` | Depends on the column type: text for character columns; decimal text for DECIMAL, NUMERIC and DECFLOAT (malformed or empty text is a binding error, never 0); `DATE`/`TIME`/`TIMESTAMP` text; `BOOLEAN` accepts `true`, `false`, `1`, `0` (case-insensitive); `BINARY`/`VARBINARY` take hex text (below). |
| `string` for `BINARY` / `VARBINARY` | Hex text, as JTOpen's `BinaryConverter.stringToBytes`: optional `0x` or `X'...'` prefix, spaces ignored, odd-length input left-padded with `0`, invalid digits are an error |
| `[]byte` | Raw bytes for `BINARY`, `VARBINARY` and `ROWID`. LOB columns are written through the LOB locator path. |
| `time.Time` | `DATE`, `TIME` or `TIMESTAMP` formatted for IBM i |
| `driver.Valuer` | Unwrapped, then mapped as above |
| `sql.Out` | Output / input-output parameter wrapper |

Parameter length rules (mirroring JTOpen):

- `TIMESTAMP` values (`time.Time` or string) are truncated to the column length, so `TIMESTAMP(6)` (length 26) keeps six fractional digits and shorter timestamp columns drop fractional digits.
- Fixed-length `BINARY` values longer than the column are silently truncated and shorter values are zero-padded (JTOpen `SQLBinary` does not raise an error).
- `VARCHAR`, `VARBINARY` and `VARGRAPHIC` values are rejected with an error when the encoded length plus the 2-byte length prefix exceeds the column length, or when the encoded length exceeds the 65535 limit of the 2-byte prefix.

## Supported

- Full `database/sql` connection lifecycle: `sql.Open`, `sql.OpenDB`, `PingContext`, `Close`, `ResetSession`, pooled reuse.
- SQL execution through `ExecContext` and `QueryContext`, with or without arguments.
- Positional (`?`) and named (`:name`) parameter placeholders. Named queries are rewritten to positional `?` before being sent to the server.
- Prepared statements with full parameter-descriptor information from the server.
- `sql.Out` for `CALL`-style stored procedures (output and input-output parameters).
- `Rows.NextResultSet` for multi-result-set cursors.
- Locator-based LOB retrieval (BLOB/CLOB/DBCLOB/XML) via `FunctionRetrieveLobData` and `FunctionFreeLob`.
- LOB input parameters (BLOB/CLOB/DBCLOB/XML) via `FunctionWriteLobData` using server-provided locators.
- Transaction begin, commit, rollback, and auto-commit toggling.
- Savepoints: `SAVEPOINT name ON ROLLBACK RETAIN CURSORS`, `ROLLBACK TO SAVEPOINT name`, and `RELEASE SAVEPOINT name`.
- Protocol-native context cancellation; deadline-based fallback when native cancel is not available.
- `CheckNamedValue` type normalization: `int`/`uint` family to `int64`, `float32` to `float64`, `time.Time` passed through, `driver.Valuer` unwrapped.
- Defined scalar aliases of integer, unsigned integer, floating-point, and string types are normalized like their underlying Go types; unsigned overflow is rejected.
- Unsigned integer parameters outside the signed `int64` range are rejected instead of being silently truncated.
- DATE/TIME/TIMESTAMP columns decoded to `time.Time` using the connection's date and time formats.
- DECFLOAT(16) and DECFLOAT(34) decoded from IEEE 754 DPD binary to a decimal string, and DECFLOAT parameters accepted as numeric or decimal text.
- Password levels 0 through 4 for signon (see Known Gaps for level 4).
- All CCSIDs that JTOpen ships conversion tables for: 168 table-driven CCSIDs (single-byte EBCDIC, ASCII and Windows code pages, bidi code pages such as 420, 424, 862, 864, 1255, 1256, EBCDIC DBCS graphic, and EBCDIC mixed SBCS/DBCS with shift-out/shift-in such as 930, 933, 935, 937, 939, 1364, 1371, 1377, 1379, 1388, 1399, 5026, 5035, 5473, 13676) plus 7 Unicode CCSIDs converted algorithmically: 1200 and 1201 (UTF-16BE), 1202 (UTF-16LE), 1208 (UTF-8), 13488, 17584, 21680 (UTF-16BE). The tables are extracted from JTOpen and verified against JTOpen-generated vectors (`internal/ccsid`). CCSIDs 0 and 65535 are treated as 37 when text is required. CCSIDs without a table return `ErrUnsupported`.
- As in JTOpen, characters that cannot be represented in a column's CCSID are replaced by the CCSID's substitution character.
- SQL error message text: with `errors=basic` (default) the message ID and first-level text are requested; with `errors=full` the second-level text is requested as well.
- Extended dynamic packages: `extendedDynamic`, `package`, `packageLibrary`, `packageCriteria`, `packageAdd`, `packageError`, and the package cache (`packageCache`).
- Close-on-EOF (V6R1 and later): the server closes the cursor at end of data, so no explicit close request is sent.
- Client info: DSN values on connect, and `SetClientInfo` at runtime (via `sql.Conn.Raw`).
- `ExecBatch` with `useBlockUpdate` (block updates; UPDATE/DELETE blocked only on V7R1 and later).
- `returnGeneratedKeys` (INSERT wrapped as `SELECT *SQLGENCOLUMNS FROM NEW TABLE(...)`, V6R1 and later).
- Socket options: `keepAlive`, `tcpNoDelay`, `receiveBufferSize`, `sendBufferSize`, `socketTimeout`.
- Query storage limit (`queryStorageLimit`) and query timeout mechanism (`queryTimeoutMechanism=cancel|qqrytimlmt`).
- All optional `RowsColumnType*` interfaces: `DatabaseTypeName`, `ScanType`, `Nullable`, `Length`, `PrecisionScale`.
- `ColumnTypeLength` reports the positive server-provided LOB maximum for LOB and LOB-locator columns, falling back to the declared column length; unknown or non-positive sizes report `ok=false`.
- `ColumnTypePrecisionScale` reports DECIMAL/NUMERIC and DECFLOAT precision and scale from server metadata.
- Connection pool support: `IsValid` and `ResetSession`.

## Known Gaps

- Bidi layout (`bidiStringType`, `bidiImplicitReordering`, `bidiNumericOrdering`) follows JTOpen's JDBC driver for the CCSIDs that JTOpen converts with a bidi table: character column values and parameters (CHAR, VARCHAR, CLOB text), SQL statement text (literals and quoted identifiers) and column names. The vectors in `testdata/bidi_driver_*.txt.gz` are generated by JTOpen (`internal/bidi/gen/BidiDriverGolden.java`) and checked by `bidi_integration_test.go`. Deviations from JTOpen: (a) each transform gets a copy of its string type's flag set, so numeral shaping does not leak into later transforms and the layout is safe for concurrent use; JTOpen shares the flag set object. (b) When marks are removed, JTOpen returns the stale tail of the source; the driver pads that tail with spaces. Not covered: JTOpen's base table, base column and schema name layout (metadata that the driver does not expose), and the numeral shaping options, which the JDBC driver does not set.
- TIMESTAMP(12) fractions (picoseconds) are truncated to nanoseconds in the default `timestampPrecision=nanoseconds` mode. Use `timestampPrecision=string` with `i400.Timestamp` to keep them.
- The client-side package cache is filled only from the package's statements when the connection opens it (RETURN_PACKAGE). Statements that the driver adds to the package during the session are not added to the cache. Driver-level statement caching (`statementCacheSize`) is separate and per connection.
- The standard `database/sql.Rows` API remains forward-only. Scrollable operations are available only through the driver-level `ScrollableRows` extension, and holdability is selected through the DSN.
- The integration suite passes against PUB400 (IBM i 7.5), covering close-on-EOF, extended dynamic packages and the package cache, block update, `returnGeneratedKeys`, `queryTimeoutMechanism` (both mechanisms), SQL error message text (prepare and EXECUTE IMMEDIATE paths), CCSID round trips including mixed DBCS, BOOLEAN, DECFLOAT, TIMESTAMP, date/time formats, client info, and CALL with multiple result sets. Not verified live: password level 4 itself (PUB400 runs password level 3; the level 4 substitute is verified against JTOpen vectors), savepoint rollback (needs a journaled library), and bidi CCSID data on a server.
- Binding errors are exposed as `*BindingError`; malformed protocol data as `*WireError`; connection dial failures as `*TransportError`. Host-server reply errors are exposed as `*ProtocolError`, while SQLCA execution errors remain `*SQLError`.

`database/sql` has no separate query-timeout method. Query timeouts are provided through `context.WithTimeout` and `ExecContext`/`QueryContext`; this is supported rather than a missing driver interface.

## Explicitly Unsupported

- Isolation levels `LevelWriteCommitted`, `LevelSnapshot`, and `LevelLinearizable` (DB2 limitation).
- CCSIDs for which JTOpen has no conversion table (JTOpen's Java-charset fallback is not replicated); these return `ErrUnsupported`.
- Prepared statement parameter shapes not recognized by the binding layer.
- `sql.Out` as a parameter to `ExecBatch`.
- Protocol-native cancel when the host metadata needed for the cancel request is not available; the driver falls back to deadline-based cancellation and invalidates the connection.

## Notes

- `ResetSession` restores autocommit, commitment control, the configured library list, and the DSN client info values between pool reuse cycles.
- `Close` sends an end-job request before closing the socket.
- The driver prefers explicit failures over silent fallback when a CCSID or binding shape is not known.
- `LastInsertId` returns the generated key that the server reports in the SQLCA (as JTOpen reads it). With `returnGeneratedKeys=true` the INSERT is wrapped so the key is read from the result set, and `RowsAffected` counts the returned rows. If no key is available, `LastInsertId` returns an error wrapping `ErrUnsupported`.
- SQL errors carry the server message: `MessageID` (for example `SQL0204`), `MessageText`, and `SecondLevelText`, plus the SQLERRMC substitution values in `Tokens`. `Error()` renders JTOpen style, for example `[SQL0204] MYTABLE in MYLIB type *FILE not found. (SQLSTATE 42704, SQLCODE -204)`. With `errors=basic` (the default) the second-level text is empty. When the SQLCA reports an error, the driver makes one extra request on the statement handle to fetch the message, as JTOpen does; if that request fails, the error falls back to the SQLSTATE summary. Errors from `ExecContext` (EXECUTE IMMEDIATE) request the message on RPB handle 0, the same handle the EXECUTE IMMEDIATE itself uses.
- `*SQLError.Is` enables class-level `errors.Is` matching on the first two characters of the SQLSTATE value; `errors.As` still works for direct field access.
- Host-server reply failures expose `ProtocolError` through `errors.As`, including the operation, return class, and return code.
- `Stmt.Close` waits for active statement executions and active result rows before closing the prepared statement on the server.
- Named parameter queries (`:name` syntax) are rewritten at prepare time; the rewritten positional query is what gets sent to IBM i.
- Bidi default: `bidiStringType` defaults to `5` as in JTOpen's JDBC driver. For the bidi CCSIDs (for example 420, 424, 862, 1255, 1256) this changes the text decoded from existing columns compared with earlier releases, which converted those tables character by character. Set `bidiStringType=-1` (not set) to use each column's CCSID string type, or `bidiStringType=0` for JTOpen's DEFAULT type.
