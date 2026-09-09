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
SCHEME://USER:PASSWORD@HOST[:PORT][/LIBRARY][?OPTION=VALUE]
```

Required:

- `SCHEME`: `as400` for an unencrypted connection or `as400s` for TLS.
- `HOST`: Hostname or IP address of the IBM i system.

Optional:

- `USER:PASSWORD`: Credentials. They are optional during DSN parsing, but the server must authenticate the connection successfully.
- `PORT`: IBM i database service port. Defaults to `8471` for `as400` and `9471` for `as400s`. The query parameters `port` and `portNumber` are also accepted.
- `/LIBRARY`: Default library/schema. If omitted, no schema is configured.
- `?OPTION=VALUE`: Optional driver settings described below.

Usernames, passwords, and library names must be URL-encoded when they contain characters such as `@`, `:`, or spaces.

### DSN options

All options are optional. Boolean values use `true` or `false`.

| Option | Values / format | Default | Description |
| --- | --- | --- | --- |
| `tls` / `secure` | `true`, `false` | Based on the scheme | Enables or disables TLS. These options override the scheme setting. |
| `naming` | `sql`, `system` | `sql` | Selects SQL naming or IBM i system naming. |
| `libraries` | Comma-separated library names | Empty | Adds libraries to the connection's library list. |
| `connectTimeout` / `timeout` | Go duration such as `5s`, or a number of seconds | `15s` | Maximum time allowed when opening the connection. |
| `statementCacheSize` | Non-negative integer | `0` | Number of prepared statements cached per connection. `0` disables statement caching. |
| `scrollableCursors` | `true`, `false` | `false` | Enables the driver's scrollable cursor extension. |
| `holdCursors` | `true`, `false` | `false` | Requests cursors that remain open across commits where supported by IBM i. |
| `dateFormat` | `julian`, `mdy`, `dmy`, `ymd`, `usa`, `iso`, `eur`, `jis`, or `0`-`7` | `iso` | Date format used by the server. |
| `dateSeparator` | `slash`, `dash`, `dot`, `comma`, `space`, or `0`-`4` | `slash` | Date separator used by the server. |
| `timeFormat` | `hms`, `usa`, `iso`, `eur`, `jis`, or `0`-`4` | `hms` | Time format used by the server. |
| `timeSeparator` | `colon`, `dot`, `comma`, `space`, or `0`-`3` | `colon` | Time separator used by the server. |
| `decimalSeparator` | `dot`, `comma`, `0`, or `1` | `dot` | Decimal separator used by the server. |
| `commitmentControl` / `commitControl` | `none`, `cs`, `chg`, `all`, `rr`, or `0`-`4` | `cs` | Sets the commitment-control level: cursor stability, changed page, all, or repeatable read. |

For example:

```text
as400://USER:PASSWORD@ibmi.example.com/MYLIB?naming=sql&libraries=APPDATA,APPLIB&connectTimeout=10s&statementCacheSize=20
```

# i400 Capability Matrix



## database/sql Surface

| Area | Status | Notes |
| --- | --- | --- |
| `sql.Open` / `sql.OpenDB` / `Connector` / `Conn` | Supported | Connection lifecycle is wired through the IBM i host-server protocols. `driver.DriverContext` and `driver.Connector` are implemented. |
| `PingContext` | Supported | Uses a lightweight test-connection request. |
| `ResetSession` | Supported | Restores autocommit, commitment control, warnings, and configured library list. |
| `BeginTx` / `Commit` / `Rollback` | Supported | Transaction state maps to auto-commit and commitment-control requests. |
| Savepoints | Supported | `SAVEPOINT`, `ROLLBACK TO SAVEPOINT`, and `RELEASE SAVEPOINT` are executed via `tx.Exec()` using IBM i DB2 SQL syntax. |
| `ExecContext` / `QueryContext` without args | Supported | Direct SQL text execution for all common statement classes. |
| `ExecContext` / `QueryContext` with positional args (`?`) | Supported | Parameters bound through the server-side descriptor path. |
| `ExecContext` / `QueryContext` with named args (`:name`) | Supported | Query rewritten to positional `?` markers before prepare; args reordered by name. |
| Prepared statements (`Prepare` / `PrepareContext`) | Supported | Positional and named placeholders; descriptor returned by server. |
| `sql.Out` (output / input-output parameters) | Supported | Callable-statement output parameters are assigned back into destinations. |
| `Rows.NextResultSet` | Supported | Multi-result-set cursors handled through cursor reuse and reopen. |
| `RowsColumnType*` metadata | Supported | Database type name, scan type, nullability, LOB length, and precision/scale (including DECFLOAT) are exposed when supplied by the server. |
| Statement caching | Opt-in | Reuses prepared statements per connection when `statementCacheSize` is greater than zero; active-use tracking, bounded FIFO eviction, and cleanup on connection close are applied. The default remains uncached. |
| Scrollable cursors | Opt-in extension | Enable with `scrollableCursors=true`; driver-level `ScrollableRows` supports `BeforeFirst`, `AfterLast`, `Current`, `First`, `Last`, `Absolute`, `Relative`, and `Row` (zero when no current row is known). Standard `database/sql.Rows` exposes only `Next`. |
| Holdable cursors | Opt-in | Enable with `holdCursors=true`; open/describe requests send IBM i hold indicators so cursors survive commit where supported by the server. |
| `Stmt.Close` | Supported | Prepared statements are closed explicitly when possible. |
| `Conn.Close` | Supported | Sends an end-job request before closing the socket. |
| Context cancellation | Supported | Protocol-native cancel when server metadata is available; deadline-based fallback otherwise. |
| Connection pooling hints | Supported | `IsValid` and `ResetSession` implemented for pool reuse. |
| `NamedValueChecker` | Supported | Normalizes `int`/`uint`/`float32`, `time.Time`, and `driver.Valuer` types automatically. |

## Type Mapping

### Result-set columns (server → Go)

| DB2 / IBM i Type | Go type returned |
| --- | --- |
| `DATE` | `time.Time` (UTC) |
| `TIME` | `time.Time` (UTC, zero date) |
| `TIMESTAMP` | `time.Time` (UTC, IBM i format `YYYY-MM-DD-HH.MM.SS[.fraction]`) |
| `CHAR`, `VARCHAR`, `LONG VARCHAR`, `GRAPHIC`, `DATALINK` | `string` |
| `DECIMAL`, `NUMERIC` | `string` (exact digits) |
| `FLOAT` (4 / 8 bytes) | `float64` |
| `BIGINT`, `INTEGER`, `SMALLINT` | `int64` |
| `DECFLOAT(16)`, `DECFLOAT(34)` | `string` (DPD-decoded decimal) |
| `BINARY`, `VARBINARY`, `ROWID` | `[]byte` |
| `BLOB`, `CLOB`, `DBCLOB`, `XML` | `[]byte` (LOB locator auto-resolved) |

### Parameter inputs (Go → server)

| Go type | Accepted as |
| --- | --- |
| `nil` | `NULL` |
| `int64`, `int`, `int8`, `int16`, `int32` | integer |
| `uint`, `uint8`, `uint16`, `uint32`, `uint64` | integer (converted to `int64` when within range; overflow is rejected) |
| `float64`, `float32` | floating-point |
| `bool` | `1` / `0` |
| `string` | text / decimal / date-time depending on column type |
| `[]byte` | binary / LOB |
| `time.Time` | `DATE` / `TIME` / `TIMESTAMP` formatted for IBM i |
| `driver.Valuer` | unwrapped, then mapped as above |
| `sql.Out` | output / input-output parameter wrapper |

## Supported

- Full `database/sql` connection lifecycle: `sql.Open`, `sql.OpenDB`, `PingContext`, `Close`, `ResetSession`, pooled reuse.
- SQL execution through `ExecContext` and `QueryContext`, with or without arguments.
- Positional (`?`) and named (`:name`) parameter placeholders — named queries are rewritten to positional `?` before being sent to the server.
- Prepared statements with full parameter-descriptor information from the server.
- `sql.Out` for `CALL`-style stored procedures (output and input-output parameters).
- `Rows.NextResultSet` for multi-result-set cursors.
- Locator-based LOB retrieval (BLOB/CLOB/DBCLOB/XML) via `FunctionRetrieveLobData` and `FunctionFreeLob`.
- LOB input parameters (BLOB/CLOB/DBCLOB/XML) via `FunctionWriteLobData` using server-provided locators.
- Transaction begin, commit, rollback, and auto-commit toggling.
- Savepoints: `SAVEPOINT name ON ROLLBACK RETAIN CURSORS`, `ROLLBACK TO SAVEPOINT name`, and `RELEASE SAVEPOINT name`.
- Protocol-native context cancellation; deadline-based fallback when native cancel is not available.
- `CheckNamedValue` type normalization: `int`/`uint` family → `int64`, `float32` → `float64`, `time.Time` passed through, `driver.Valuer` unwrapped.
- Defined scalar aliases of integer, unsigned integer, floating-point, and string types are normalized like their underlying Go types; unsigned overflow is rejected.
- Unsigned integer parameters outside the signed `int64` range are rejected instead of being silently truncated.
- DATE/TIME/TIMESTAMP columns decoded to `time.Time` (IBM i timestamp format including variable fractional-second precision).
- DECFLOAT(16) and DECFLOAT(34) decoded from IEEE 754 DPD binary to decimal string.
- SQLSTATE class sentinel errors (`ErrSQLNoData`, `ErrSQLDataException`, `ErrSQLConstraintViolation`, `ErrSQLInvalidCursor`, `ErrSQLSyntaxError`) for `errors.Is` matching.
- Explicit CCSID handling: 37 (EBCDIC), 273 (treated as EBCDIC-37), 1200/13488 (UTF-16BE), 1208 (UTF-8); CCSIDs 0 and 65535 remapped to 37.
- All optional `RowsColumnType*` interfaces: `DatabaseTypeName`, `ScanType`, `Nullable`, `Length`, `PrecisionScale`.
- `ColumnTypeLength` reports the positive server-provided LOB maximum for LOB and LOB-locator columns, falling back to the declared column length; unknown or non-positive sizes report `ok=false`.
- `ColumnTypePrecisionScale` reports DECIMAL/NUMERIC and DECFLOAT precision and scale from server metadata.
- Connection pool support: `IsValid` and `ResetSession`.

## Known Gaps

- IBM i package caching is not implemented. Driver-level statement caching is available as an opt-in per-connection feature via `statementCacheSize`; cached statements are never shared across connections.
- The standard `database/sql.Rows` API remains forward-only. Scrollable operations are available only through the driver-level `ScrollableRows` extension, and holdability is selected through the DSN/configuration.
- Binding errors are exposed as `*BindingError`; malformed protocol data is exposed as `*WireError`; connection dial failures are exposed as `*TransportError`. Host-server reply errors are exposed as `*ProtocolError`, while SQLCA execution errors remain `*SQLError`.

`database/sql` has no separate query-timeout method. Query timeouts are intentionally provided through `context.WithTimeout` and `ExecContext`/`QueryContext`; this is supported rather than a missing driver interface.

## Explicitly Unsupported

- Isolation levels `LevelWriteCommitted`, `LevelSnapshot`, and `LevelLinearizable` (DB2 limitation).
- CCSIDs outside the explicit encoding table.
- Prepared statement parameter shapes not recognized by the binding layer.
- Protocol-native cancel when the host metadata needed for the cancel request is not available; the driver falls back to deadline-based cancellation and invalidates the connection.

## Notes

- `ResetSession` restores autocommit, commitment control, and configured library list state between pool reuse cycles.
- `Close` sends an end-job request before closing the socket.
- The driver prefers explicit failures over silent fallback when a CCSID or binding shape is not known.
- `*SQLError.Is` enables class-level `errors.Is` matching on the first two characters of the SQLSTATE value; `errors.As` still works for direct field access.
- Host-server reply failures expose `ProtocolError` through `errors.As`, including the operation, return class, and return code.
- `Stmt.Close` waits for active statement executions and active result rows before closing the prepared statement on the server.
- Named parameter queries (`:name` syntax) are rewritten at prepare time; the rewritten positional query is what gets sent to IBM i.
