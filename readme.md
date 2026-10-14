# i400

`i400` is a pure Go `database/sql` driver for Db2 for IBM i. It talks to the IBM i database host server directly (no ODBC, no CGO) and follows the protocol and behaviour of [JTOpen](https://github.com/IBM/JTOpen), IBM's open-source JDBC driver for IBM i.

Highlights:

- Full `database/sql` support: connection pooling, transactions, savepoints, prepared statements, named parameters, `sql.Out`, `Rows.NextResultSet`, context cancellation, column metadata.
- All 175 CCSIDs that JTOpen ships conversion tables for, including EBCDIC mixed SBCS/DBCS and bidi code pages, verified byte for byte against JTOpen.
- SQL errors with the server's message text (`[SQL0204] ... not found.`).
- JTOpen features such as extended dynamic SQL packages with package cache, block insert/update (`ExecBatch`), client info, generated keys, query timeout mechanisms, password levels 0 to 4, and TLS.
- Tested against a live IBM i 7.5 system (PUB400), see [Verification](#verification).

## Installation

```sh
go get github.com/bashdi/i400
```

The driver registers itself as `i400`. Go 1.24 or later is required.

## Usage

```go
package main

import (
	"context"
	"database/sql"
	"errors"
	"log"

	"github.com/bashdi/i400"
)

func main() {
	db, err := sql.Open("i400", "as400://USER:PASSWORD@ibmi.example.com/MYLIB")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	rows, err := db.QueryContext(ctx, "SELECT ID, NAME FROM CUSTOMER WHERE CITY = ?", "Berlin")
	if err != nil {
		var sqlErr *i400.SQLError
		if errors.As(err, &sqlErr) {
			log.Fatalf("%s: %s", sqlErr.MessageID, sqlErr.MessageText)
		}
		log.Fatal(err)
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			log.Fatal(err)
		}
		log.Println(id, name)
	}
	if err := rows.Err(); err != nil {
		log.Fatal(err)
	}
}
```

Use `as400s://` for a TLS connection. To build a `*sql.DB` from a connector, pass the connector returned by `(&i400.Driver{}).OpenConnector(dsn)` to `sql.OpenDB`.

## DSN format

```text
SCHEME://USER:PASSWORD@HOST[:PORT][/LIBRARY][?OPTION=VALUE&...]
```

Required:

- `SCHEME`: `as400` for an unencrypted connection or `as400s` for TLS.
- `HOST`: Hostname or IP address of the IBM i system.

Optional:

- `USER:PASSWORD`: Credentials. They are optional during DSN parsing, but the server must authenticate the connection.
- `PORT`: Database host server port. Defaults to `8471` for `as400` and `9471` for `as400s`. The `port` and `portNumber` options are also accepted.
- `/LIBRARY`: Default schema (library). If omitted, no schema is configured.
- `?OPTION=VALUE`: Driver options, described below.

User names, passwords and library names must be URL-encoded when they contain characters such as `@`, `:` or spaces. `ParseDSN` parses and validates a DSN without connecting.

### DSN options

All options are optional. Boolean values are `true` or `false` (parsed by `strconv.ParseBool`, so `1`/`0` also work). Invalid values are rejected with `ErrInvalidDSN`.

Options shown with a JTOpen spelling in the tables (for example `query optimize goal`) are matched ignoring case, spaces, underscores and dashes, so `queryOptimizeGoal`, `query optimize goal` and `query_optimize_goal` are equivalent. Options shown with a single key only (`tls`, `secure`, `naming`, `dateFormat`, `dateSeparator`, `timeFormat`, `timeSeparator`, `decimalSeparator`, `commitmentControl`, `commitControl`, `libraries`, `statementCacheSize`, `scrollableCursors`, `holdCursors`, `bidiImplicitReordering`, `bidiNumericOrdering`, `connectTimeout`, `timeout`, `port`, `portNumber`) accept only that key.

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
| `errors` | `basic`, `full` | `basic` | SQL error text detail. `basic` requests the message ID and first-level text. `full` also requests the second-level text. See [Errors](#errors). |
| `bidiStringType` / `bidi string type` | `0`, `4` to `11`, or `-1` (`none`, or an empty value: not set) | `5` | Bidi layout of character data in the CCSIDs that JTOpen converts with a bidi table (for example 420, 424, 862, 916, 1255, 1256). The default is `5` as in JTOpen. Not set uses each column's CCSID string type, as JTOpen's JDBC driver does. The same setting drives the layout of SQL statement text and column names. |
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
| `useBlockUpdate` / `use block update` | `true`, `false` | `false` | JTOpen "use block update". When `true`, `Conn.ExecBatch` and `Stmt.ExecBatch` send eligible rows as parameter blocks. See [Batch execution](#batch-execution). |
| `queryStorageLimit` / `query storage limit` | Integer bytes; `-1` (`*NOMAX`) or a positive value | `-1` | Query storage limit (V6R1 and later). `-1` is not sent. Values below `-1` act as `-1`; values above 2147352578 act as 2147352578. |
| `queryTimeoutMechanism` / `query timeout mechanism` | `cancel`, `qqrytimlmt` | `cancel` | How a context deadline is enforced. `cancel` uses only the driver's cancel when the deadline expires. `qqrytimlmt` also sends the remaining time to the server as query time limit. See [Query timeouts](#query-timeouts). |

#### Generated keys

| Option | Values / format | Default | Description |
| --- | --- | --- | --- |
| `returnGeneratedKeys` / `return generated keys` | `true`, `false` | `false` | When `true`, an `INSERT` run through `Exec`/`ExecContext` is wrapped as `SELECT *SQLGENCOLUMNS FROM NEW TABLE(<insert>)` (V6R1 and later, as in JTOpen) so `LastInsertId` returns the generated key and `RowsAffected` counts the returned rows. This changes the SQL that runs. Other statements are not changed. |

#### Extended dynamic packages

| Option | Values / format | Default | Description |
| --- | --- | --- | --- |
| `extendedDynamic` / `extended dynamic` | `true`, `false` | `false` | Stores prepared statements in an IBM i SQL package, as in JTOpen. |
| `package` | Package name | Empty | Package name. Only the first six characters are used, upper-cased, with blanks replaced by underscores; a four-character suffix derived from the connection settings is appended, as in JTOpen. Required when `extendedDynamic=true`. |
| `packageLibrary` / `package library` | Library name | `QGPL` | Library that holds the package. Upper-cased. |
| `packageCriteria` / `package criteria` | `default`, `select` | `default` | Which statements are packaged. `default` packages statements with parameter markers, `INSERT ... SELECT`, `SELECT ... FOR UPDATE`, and `DECLARE`. `select` also packages every `SELECT`, `WITH`, and `VALUES`. |
| `packageAdd` / `package add` | `true`, `false` | `true` | Allows the server to add statements to the package. Sent on every connection, as JTOpen does. |
| `packageError` / `package error` | `none`, `warning`, `exception` | `warning` | What happens when the package cannot be created or used. `warning` records an SQL warning and continues without packaging, `exception` fails the statement, `none` continues silently. |
| `packageCCSID` / `package ccsid` | Positive CCSID, or `system` | `13488` | CCSID used to encode SQL statement text sent to the server, and the tag sent with it. `13488` and `1200` send UTF-16BE with that tag (`1200` falls back to `13488` before V5R3). `system` encodes with the server job CCSID from signon (`37` if unknown). Other CCSIDs encode the text with their conversion table. As in JTOpen, this applies with or without extended dynamic. |
| `packageCache` / `package cache` | `true`, `false` | `false` | Client-side package cache. Only applies with `extendedDynamic=true`. When the package already exists, its statements are downloaded and a statement whose text matches a cached statement is used without a prepare, as in JTOpen. Statements whose parameter formats contain LOBs are always prepared again. |
| `packageClear` / `package clear` | `true`, `false` | `false` | Accepted for JTOpen compatibility and has no effect. JTOpen no longer sends it; the server clears full packages itself. |

#### Client info

These values are sent to the server on connect when set (V6R1 and later). They can be changed at runtime with `Conn.SetClientInfo`.

| Option | Values / format | Default | Description |
| --- | --- | --- | --- |
| `applicationName` / `application name` | Text, up to 255 bytes | Empty | Client info `ApplicationName`. |
| `clientUser` / `client user` | Text, up to 255 bytes | Empty | Client info `ClientUser`. |
| `clientAccounting` / `client accounting` | Text, up to 255 bytes | Empty | Client info `ClientAccounting`. |
| `clientHostname` / `client hostname` | Text, up to 255 bytes | Empty | Client info `ClientHostname`. |
| `clientProgramID` / `client program id` | Text, up to 255 bytes | Empty | Client info `ClientProgramID`. |

Example:

```text
as400://USER:PASSWORD@ibmi.example.com/MYLIB?naming=sql&libraries=APPDATA,APPLIB&connectTimeout=10s&statementCacheSize=20&errors=full
```

## Driver extensions

Some JTOpen features have no `database/sql` API. They are methods on `*i400.Conn` and `*i400.Stmt`, reached through `sql.Conn.Raw`:

```go
conn, err := db.Conn(ctx)
if err != nil {
	return err
}
defer conn.Close()

err = conn.Raw(func(driverConn any) error {
	c := driverConn.(*i400.Conn)
	// use c.ExecBatch, c.SetClientInfo, c.Warnings, ...
	return nil
})
```

| Method | Purpose |
| --- | --- |
| `Conn.ExecBatch(ctx, query, rows)`, `Stmt.ExecBatch(ctx, rows)` | Run one statement for many parameter rows; see [Batch execution](#batch-execution). |
| `Conn.SetClientInfo(ctx, name, value)`, `Conn.ClientInfo(name)` | Set or read client info; see [Client info at runtime](#client-info-at-runtime). |
| `Conn.Warnings()`, `Conn.ClearWarnings()` | SQL warnings of the last operation as a `*SQLWarning` chain. |
| `ScrollableRows` (interface on rows) | Scrollable cursor operations when `scrollableCursors=true`. |

The types `i400.Timestamp` and `i400.NullTimestamp` scan and bind TIMESTAMP values with picosecond precision (TIMESTAMP(12)).

### Batch execution

`ExecBatch` prepares the statement once, runs it for every row and returns the total rows affected:

```go
err = conn.Raw(func(driverConn any) error {
	c := driverConn.(*i400.Conn)
	n, err := c.ExecBatch(ctx, "INSERT INTO MYLIB.ORDERS VALUES(?, ?, ?)", [][]driver.Value{
		{int64(1), "widget", 3.5},
		{int64(2), "gadget", nil},
		{int64(3), "gizmo", 7.25},
	})
	log.Println("rows:", n)
	return err
})
```

Without `useBlockUpdate=true`, each row is one EXECUTE on the prepared statement. With `useBlockUpdate=true`, eligible rows are sent in blocks, as JTOpen does with "use block update":

- Eligible are `INSERT ... VALUES(?, ...)` with only parameter markers in the VALUES clause, and `UPDATE` or `DELETE`. As in JTOpen, `UPDATE` and `DELETE` are blocked only on V7R1 and later; on older or unknown servers they run row by row. Statements with LOB parameters are never blocked.
- A block holds at most 32000 rows and about 1 MiB of parameter data. Consecutive rows with the same encoded shape share a block; a single larger row is sent alone.
- The result is the sum of the block update counts. If a block fails, the error is returned together with the count of the blocks that succeeded.

Row values bind to the placeholders in the order they appear in the query, including named placeholders. Values are normalized like normal parameters. `sql.Out` is rejected.

### Client info at runtime

`Conn.SetClientInfo(ctx, name, value)` sets one client info value on the server:

```go
err := conn.Raw(func(driverConn any) error {
	return driverConn.(*i400.Conn).SetClientInfo(ctx, "ClientUser", "ALICE")
})
```

Names are case-sensitive, as in JTOpen: `ApplicationName`, `ClientUser`, `ClientAccounting`, `ClientHostname`, `ClientProgramID`. An unknown name returns `ErrUnknownClientInfo`. `Conn.ClientInfo(name)` returns the current value, or `""` for an unknown name. Below V6R1 the value is kept on the client only, as in JTOpen. `ResetSession` (pool reuse) restores the DSN values, so a runtime change does not leak to the next user of the connection.

### Query timeouts

`database/sql` has no query-timeout method; use a context deadline (`context.WithTimeout`) with `QueryContext` or `ExecContext`. When the deadline expires, the driver cancels the running statement with the host server's native cancel request and returns `context.DeadlineExceeded`. If native cancel is not available, the connection is invalidated instead.

With `queryTimeoutMechanism=qqrytimlmt` the driver also sends the remaining time (whole seconds, at least 1) to the server before each open or execute, as JTOpen does. This is the IBM i query time limit (QQRYTIMLMT): the server compares the optimizer's estimated run time with the limit before running the query and rejects it with SQL0666 if it is over. It does not stop a query that is already running.

## Type mapping

### Result-set columns (server to Go)

| Db2 for i type | Go type returned |
| --- | --- |
| `CHAR`, `VARCHAR`, `LONG VARCHAR`, `GRAPHIC`, `VARGRAPHIC`, `LONG VARGRAPHIC`, `DATALINK` | `string`, decoded from the column CCSID |
| `CLOB`, `DBCLOB`, `XML` | `string`, decoded from the column CCSID. CCSID 65535 returns `[]byte`. A leading BOM is removed from XML text. |
| `BLOB`, `BINARY`, `VARBINARY`, `ROWID` | `[]byte` |
| `SMALLINT`, `INTEGER`, `BIGINT` | `int64` |
| `REAL`, `DOUBLE` / `FLOAT` | `float64` |
| `DECIMAL`, `NUMERIC` | `string` with the exact digits |
| `DECFLOAT(16)`, `DECFLOAT(34)` | `string` with the decimal value |
| `BOOLEAN` | `bool` (`0xF0` is false, any other byte is true) |
| `DATE` | `time.Time` (UTC), parsed with the connection's `dateFormat` and `dateSeparator` |
| `TIME` | `time.Time` (UTC, year 0, January 1), parsed with `timeFormat` and `timeSeparator`. A blank value is returned as an empty string. |
| `TIMESTAMP` | Default (`timestampPrecision=nanoseconds`): `time.Time` (UTC); fractions finer than nanoseconds are truncated, not rounded. With `timestampPrecision=string`: the IBM i text with all digits; scan it into `i400.Timestamp`, `i400.NullTimestamp` or `string`. |

LOB locator columns are resolved automatically and decoded like the LOB types above. For stored procedure result sets on V6R1 and later, DATE and TIME use the date and time format reported with the result set, as in JTOpen.

### Parameters (Go to server)

| Go type | Accepted as |
| --- | --- |
| `nil` | `NULL` |
| `int64`, `int`, `int8`, `int16`, `int32` | Integer. Values out of range for the column type are rejected. |
| `uint`, `uint8`, `uint16`, `uint32`, `uint64` | Integer, converted to `int64` when in range; overflow is rejected. |
| `float64`, `float32` | Floating point (`float32` is converted to `float64`). For DECFLOAT columns the value is formatted as decimal text. |
| `bool` | `BOOLEAN` columns: `0xF1` for true, `0xF0` for false. Numeric columns: `1` or `0`. |
| `string` | Text for character columns. Decimal text for numeric columns: malformed or empty text is a binding error, never 0, and extra DECIMAL fraction digits are truncated as in JTOpen. Date, time and timestamp text. `true`, `false`, `1`, `0` (case-insensitive) for `BOOLEAN`. Hex text for `BINARY` and `VARBINARY` (below). |
| `string` for `BINARY` / `VARBINARY` | Hex text, as JTOpen's `BinaryConverter.stringToBytes`: optional `0x` or `X'...'` prefix, spaces ignored, odd-length input left-padded with `0`, invalid digits are an error. |
| `[]byte` | Raw bytes for `BINARY`, `VARBINARY`, `ROWID` and `BLOB`. |
| `time.Time` | `DATE`, `TIME` or `TIMESTAMP`, sent in ISO format as JTOpen does. |
| `i400.Timestamp` | `TIMESTAMP` with up to 12 fraction digits. |
| `driver.Valuer` | Unwrapped, then mapped as above. |
| `sql.Out` | Output or input/output parameter of a `CALL`. |

LOB parameters are sent inline when the server describes them without a locator, and through the server-provided LOB locator otherwise.

Length rules (as in JTOpen):

- `TIMESTAMP` values are truncated to the column length, so `TIMESTAMP(6)` keeps six fraction digits.
- Fixed-length `BINARY` values longer than the column are truncated and shorter values are zero-padded.
- `CHAR`, `VARCHAR`, `VARBINARY` and `VARGRAPHIC` values that do not fit the column are rejected with an error, as are values longer than 65535 bytes for the 2-byte length prefix.

### Character conversion

- All CCSIDs that JTOpen ships conversion tables for: 168 table-driven CCSIDs (single-byte EBCDIC, ASCII and Windows code pages, bidi code pages such as 420, 424, 862, 864, 1255, 1256, EBCDIC DBCS graphic, and EBCDIC mixed SBCS/DBCS with shift-out/shift-in such as 930, 933, 935, 937, 939, 1364, 1371, 1377, 1379, 1388, 1399, 5026, 5035, 5473, 13676) plus 7 Unicode CCSIDs: 1200 and 1201 (UTF-16BE), 1202 (UTF-16LE), 1208 (UTF-8), 13488, 17584 and 21680 (UTF-16BE).
- The tables are extracted from JTOpen (`internal/ccsid`) and checked against vectors generated by JTOpen.
- As in JTOpen, characters that cannot be represented in a CCSID are replaced by its substitution character.
- CCSIDs 0 and 65535 are treated as 37 when text is required; character columns with CCSID 65535 are returned as `[]byte`. CCSIDs without a JTOpen table return `ErrUnsupported`.
- Bidi CCSIDs get JTOpen's bidi layout transformation (Arabic shaping, reordering), controlled by `bidiStringType`, `bidiImplicitReordering` and `bidiNumericOrdering`. As in JTOpen's JDBC driver, it applies to character column values and parameters, SQL statement text and column names.

## Errors

| Error | When |
| --- | --- |
| `*SQLError` | The server reports an SQL error (negative SQLCODE). Fields: `Code` (SQLCODE), `State` (SQLSTATE), `MessageID` (for example `SQL0204`), `MessageText`, `SecondLevelText` (with `errors=full`), `Tokens` (SQLERRMC values). |
| `*BindingError` | A parameter cannot be converted to the column type. |
| `*ProtocolError` | The host server reports a failure without SQL diagnostics. Includes the operation, error class and return code. |
| `*WireError` | Malformed or oversized data on the wire. |
| `*TransportError` | The connection could not be established. |

`SQLError.Error()` is formatted like JTOpen's messages, for example `[SQL0204] MYTABLE in MYLIB type *FILE not found. (SQLSTATE 42704, SQLCODE -204)`. To get the message text, the driver sends one follow-up request after an SQL error, as JTOpen does; if that request fails, the error contains only SQLSTATE and SQLCODE.

`errors.Is` matches SQLSTATE classes with the sentinels `ErrSQLNoData` (02), `ErrSQLDataException` (22), `ErrSQLConstraintViolation` (23), `ErrSQLInvalidCursor` (24/25) and `ErrSQLSyntaxError` (42). Further sentinels: `ErrAuthentication`, `ErrConnection`, `ErrConnectionClosed`, `ErrInvalidDSN`, `ErrUnsupported`.

SQL warnings (positive SQLCODE) do not fail the call; they are available from `Conn.Warnings()`.

## Capability matrix

| Area | Status | Notes |
| --- | --- | --- |
| `sql.Open` / `sql.OpenDB` / `Connector` / `Conn` | Supported | `driver.DriverContext` and `driver.Connector` are implemented. |
| Signon | Supported | Password levels 0 to 4 (DES, SHA-1, PBKDF2-SHA-512), TLS with `as400s://`. |
| `PingContext` | Supported | Lightweight test-connection request. |
| `ResetSession` / `IsValid` | Supported | Restores autocommit, commitment control, warnings, library list and DSN client info for pool reuse. |
| `BeginTx` / `Commit` / `Rollback` | Supported | Isolation levels map to IBM i commitment control. |
| Savepoints | Supported | `SAVEPOINT`, `ROLLBACK TO SAVEPOINT` and `RELEASE SAVEPOINT` through `tx.Exec`. |
| `ExecContext` / `QueryContext` | Supported | With or without arguments; positional (`?`) and named (`:name`) placeholders. |
| Prepared statements | Supported | Parameter descriptions from the server. Up to 9998 open statements per connection (JTOpen's limit); statement ids are reused after close. |
| `sql.Out` | Supported | Output and input/output parameters of `CALL`. |
| `Rows.NextResultSet` | Supported | Stored procedures returning several result sets. |
| Column metadata | Supported | `DatabaseTypeName`, `ScanType`, `Nullable`, `Length`, `PrecisionScale`. |
| LOBs | Supported | BLOB, CLOB, DBCLOB and XML, inline or through locators, in both directions. |
| Context cancellation | Supported | Native host server cancel; see [Query timeouts](#query-timeouts). |
| Close-on-EOF | Supported | V6R1 and later: the server closes forward-only cursors at end of data, saving a request. |
| Statement caching | Opt-in | `statementCacheSize`, per connection. |
| Extended dynamic packages | Opt-in | `extendedDynamic`, `package`, `packageCache` and related options. |
| Scrollable cursors | Opt-in | `scrollableCursors=true` and the `ScrollableRows` interface (`BeforeFirst`, `AfterLast`, `Current`, `First`, `Last`, `Absolute`, `Relative`, `Row`). |
| Holdable cursors | Opt-in | `holdCursors=true`. |
| Batch execution | Extension | `ExecBatch`, with `useBlockUpdate=true` for block insert/update. |
| Generated keys | Supported | `LastInsertId` reads the key from the SQLCA; `returnGeneratedKeys=true` wraps INSERTs as `SELECT *SQLGENCOLUMNS FROM NEW TABLE(...)` (V6R1 and later). Without a key, `LastInsertId` returns an error wrapping `ErrUnsupported`. |
| Client info | Supported | DSN options and `SetClientInfo`. |
| `NamedValueChecker` | Supported | Normalizes integer and float types, `time.Time` and `driver.Valuer`. |

## Verification

- Unit tests run without a server (`go test -short ./...`). Conversion tables, DECFLOAT encoding, password substitutes, SQL package names and bidi transformations are checked against vectors generated by running JTOpen itself.
- The integration suite passes against PUB400 (IBM i 7.5, password level 3, CCSID 273). It covers close-on-EOF, extended dynamic packages and the package cache, block insert/update, `returnGeneratedKeys`, both query timeout mechanisms, SQL error message text, CCSID round trips including mixed DBCS, BOOLEAN, DECFLOAT, TIMESTAMP, date and time formats, client info, `CALL` with several result sets and the reuse of statement ids.
- Not verified on a live system: password level 4 (checked against JTOpen vectors only), savepoint rollback (needs a journaled library; the PUB400 test library is not journaled) and bidi CCSID data stored on a server (checked against JTOpen vectors only).

## Known gaps and differences from JTOpen

- The standard `database/sql.Rows` API is forward-only; scrolling needs the `ScrollableRows` extension.
- With the default `timestampPrecision=nanoseconds`, TIMESTAMP(12) fractions are truncated to nanoseconds. Use `timestampPrecision=string` with `i400.Timestamp` to keep them.
- The package cache is filled when the connection first uses the package. Statements added to the package later in the session are not added to the cache.
- Bidi: each transformation uses its own copy of the bidi flags, so numeral shaping does not leak between transformations and conversion is safe for concurrent use (JTOpen shares one flag set). When directional marks are removed, the remaining length is padded with spaces where JTOpen leaves stale characters. Base table, column and schema names are not transformed because the driver does not expose them.
- `bidiStringType` defaults to `5` as in JTOpen. Compared with earlier releases of this driver, which converted bidi CCSIDs character by character, text from bidi CCSID columns can be ordered differently. Set `bidiStringType=-1` to use each column's CCSID string type.
- `queryTimeoutMechanism` defaults to `cancel`; JTOpen defaults to `qqrytimlmt`.
- Client info: the non-empty DSN values are also sent on connect, which JTOpen only does through `setClientInfo`. Values are encoded as CCSID 37, and values longer than 255 bytes are an error.

## Unsupported

- Isolation levels `LevelWriteCommitted`, `LevelSnapshot` and `LevelLinearizable`.
- CCSIDs for which JTOpen has no conversion table (JTOpen's fallback to Java charsets is not replicated).
- `sql.Out` parameters in `ExecBatch`.

## Development

### Running the tests

```sh
go test -short ./...   # unit tests, no server needed
go test ./...          # also runs the integration tests when a system is configured
```

The integration tests (`integration_*_test.go`, `database_sql_crud_test.go`) run against a live IBM i system. They are skipped when no system is configured and with `-short`.

The simplest setup is a `.env` file in the repository root. It is listed in `.gitignore`. Copy `.env_template` to `.env` and fill it in:

```text
I400_ADRESSE=pub400.com
I400_USER=MYUSER
I400_PASSWORT=secret
I400_BIBLIOTHEK=MYUSER1
```

The tests build the DSN `as400://I400_USER:I400_PASSWORT@I400_ADRESSE` and create their objects in the library `I400_BIBLIOTHEK`. Each test drops what it creates. Variables already set in the environment take precedence over `.env`.

Instead of `.env`, the environment variables `I400_TEST_DSN` (a complete DSN) and `I400_TEST_LIBRARY` can be set:

```powershell
$env:I400_TEST_DSN = "as400://USER:PASSWORD@ibmi.example.com/?naming=sql"
$env:I400_TEST_LIBRARY = "ITTEST"
go test -count=1 -v -run 'Integration|DatabaseSQLCRUD' ./...
```

```bash
export I400_TEST_DSN='as400://USER:PASSWORD@ibmi.example.com/?naming=sql'
export I400_TEST_LIBRARY=ITTEST
go test -count=1 -v -run 'Integration|DatabaseSQLCRUD' ./...
```

Tests that create objects are skipped when no library is configured. The savepoint tests need a library with journaled tables and are skipped otherwise.

### Regenerating tables from JTOpen

The CCSID tables (`internal/ccsid/tables`) and the bidi tables (`internal/bidi`) are generated from a JTOpen source checkout and are not edited by hand. The generators are Java programs in `internal/ccsid/gen` and `internal/bidi/gen`; the commands are documented at the top of `internal/ccsid/ccsid.go` and in the header comment of each generator in `internal/bidi/gen`. The same programs write the JTOpen-generated test vectors in the `testdata` directories. A JDK and a JTOpen checkout are needed only for regenerating, not for building or testing.
