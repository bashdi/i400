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
