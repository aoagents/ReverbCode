package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	moderncsqlite "modernc.org/sqlite"
)

const driverName = "ao-sqlite"

var registerDriverOnce sync.Once

func init() {
	ensureDriverRegistered()
}

func ensureDriverRegistered() {
	registerDriverOnce.Do(func() {
		sql.Register(driverName, &aoDriver{base: &moderncsqlite.Driver{}})
	})
}

// dupNumericOffsetPattern matches Go time.Time.String() values produced when a
// time.Time carries an unnamed fixed-offset Location (for example,
// "2026-10-17 11:19:16.938849 -0400 -0400"), which modernc.org/sqlite's
// built-in "2006-01-02 15:04:05.999999999 -0700 MST" layout rejects because
// the trailing zone token is numeric rather than an alphabetic abbreviation.
var dupNumericOffsetPattern = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2}(?:\.\d+)?\s+[+-]\d{4})\s+[+-]\d{4}$`)

var fallbackTimeLayouts = []string{
	"2006-01-02 15:04:05.999999999 -0700 MST",
	"2006-01-02 15:04:05.999999999 -0700",
	"2006-01-02 15:04:05.999999999 -07:00",
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999-0700",
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05.999999999-07:00",
	"2006-01-02T15:04:05.999999999-0700",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04",
	"2006-01-02T15:04",
	"2006-01-02",
}

// ParseTimestamp normalizes any SQLite TIMESTAMP/DATETIME/DATE representation
// into a UTC time.Time. If the stored value is empty or unparseable corrupt
// text, it returns the zero time.Time{} so a damaged cell cannot fail
// database/sql row scanning and take down read endpoints.
func ParseTimestamp(raw string) (time.Time, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return time.Time{}, false
	}
	if idx := strings.Index(s, " m="); idx > 0 {
		s = strings.TrimSpace(s[:idx])
	}
	if m := dupNumericOffsetPattern.FindStringSubmatch(s); len(m) == 2 {
		if t, err := time.Parse("2006-01-02 15:04:05.999999999 -0700", m[1]); err == nil {
			return t.UTC(), true
		}
		if t, err := time.Parse("2006-01-02T15:04:05.999999999 -0700", m[1]); err == nil {
			return t.UTC(), true
		}
	}
	for _, layout := range fallbackTimeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return unixIntToUTC(n), true
	}
	return time.Time{}, false
}

func unixIntToUTC(v int64) time.Time {
	switch {
	case v > 1e17 || v < -1e17:
		return time.Unix(0, v).UTC()
	case v > 1e14 || v < -1e14:
		return time.UnixMicro(v).UTC()
	case v > 1e11 || v < -1e11:
		return time.UnixMilli(v).UTC()
	default:
		return time.Unix(v, 0).UTC()
	}
}

type aoDriver struct {
	base driver.Driver
}

func (d *aoDriver) Open(name string) (driver.Conn, error) {
	c, err := d.base.Open(name)
	if err != nil {
		return nil, err
	}
	return &aoConn{Conn: c}, nil
}

type aoConn struct {
	driver.Conn
}

func (c *aoConn) CheckNamedValue(nv *driver.NamedValue) error {
	switch v := nv.Value.(type) {
	case time.Time:
		nv.Value = v.UTC()
		return nil
	case driver.Valuer:
		val, err := v.Value()
		if err != nil {
			return err
		}
		if t, ok := val.(time.Time); ok {
			nv.Value = t.UTC()
		} else {
			nv.Value = val
		}
		return nil
	}
	if checker, ok := c.Conn.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}

func (c *aoConn) Ping(ctx context.Context) error {
	if p, ok := c.Conn.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

func (c *aoConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if b, ok := c.Conn.(driver.ConnBeginTx); ok {
		return b.BeginTx(ctx, opts)
	}
	return c.Conn.Begin() //nolint:staticcheck // fallback if ConnBeginTx is absent
}

func (c *aoConn) Prepare(query string) (driver.Stmt, error) {
	st, err := c.Conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &aoStmt{Stmt: st}, nil
}

func (c *aoConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if p, ok := c.Conn.(driver.ConnPrepareContext); ok {
		st, err := p.PrepareContext(ctx, query)
		if err != nil {
			return nil, err
		}
		return &aoStmt{Stmt: st}, nil
	}
	return c.Prepare(query)
}

func (c *aoConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	normalizeNamedArgs(args)
	if e, ok := c.Conn.(driver.ExecerContext); ok {
		return e.ExecContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}

func (c *aoConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	normalizeNamedArgs(args)
	if q, ok := c.Conn.(driver.QueryerContext); ok {
		rows, err := q.QueryContext(ctx, query, args)
		if err != nil {
			return nil, err
		}
		return wrapRows(rows), nil
	}
	return nil, driver.ErrSkip
}

type aoStmt struct {
	driver.Stmt
}

func (s *aoStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	normalizeNamedArgs(args)
	if e, ok := s.Stmt.(driver.StmtExecContext); ok {
		return e.ExecContext(ctx, args)
	}
	vals := make([]driver.Value, len(args))
	for i, a := range args {
		vals[i] = a.Value
	}
	return s.Stmt.Exec(vals) //nolint:staticcheck // fallback if StmtExecContext is absent
}

func (s *aoStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	normalizeNamedArgs(args)
	if q, ok := s.Stmt.(driver.StmtQueryContext); ok {
		rows, err := q.QueryContext(ctx, args)
		if err != nil {
			return nil, err
		}
		return wrapRows(rows), nil
	}
	vals := make([]driver.Value, len(args))
	for i, a := range args {
		vals[i] = a.Value
	}
	rows, err := s.Stmt.Query(vals) //nolint:staticcheck // fallback if StmtQueryContext is absent
	if err != nil {
		return nil, err
	}
	return wrapRows(rows), nil
}

func (s *aoStmt) Query(args []driver.Value) (driver.Rows, error) {
	for i, v := range args {
		if t, ok := v.(time.Time); ok {
			args[i] = t.UTC()
		}
	}
	rows, err := s.Stmt.Query(args) //nolint:staticcheck // required by driver.Stmt
	if err != nil {
		return nil, err
	}
	return wrapRows(rows), nil
}

func normalizeNamedArgs(args []driver.NamedValue) {
	for i := range args {
		if t, ok := args[i].Value.(time.Time); ok {
			args[i].Value = t.UTC()
		}
	}
}

type aoRows struct {
	driver.Rows
	declType driver.RowsColumnTypeDatabaseTypeName
}

func wrapRows(r driver.Rows) driver.Rows {
	if r == nil {
		return nil
	}
	dt, _ := r.(driver.RowsColumnTypeDatabaseTypeName)
	return &aoRows{Rows: r, declType: dt}
}

func (r *aoRows) Next(dest []driver.Value) error {
	if err := r.Rows.Next(dest); err != nil {
		return err
	}
	for i, val := range dest {
		if t, ok := val.(time.Time); ok {
			dest[i] = t.UTC()
			continue
		}
		if r.declType == nil {
			continue
		}
		switch r.declType.ColumnTypeDatabaseTypeName(i) {
		case "DATE", "DATETIME", "TIMESTAMP":
			switch v := val.(type) {
			case string:
				t, _ := ParseTimestamp(v)
				dest[i] = t
			case []byte:
				t, _ := ParseTimestamp(string(v))
				dest[i] = t
			case int64:
				dest[i] = unixIntToUTC(v)
			}
		}
	}
	return nil
}

func (r *aoRows) ColumnTypeDatabaseTypeName(index int) string {
	if r.declType != nil {
		return r.declType.ColumnTypeDatabaseTypeName(index)
	}
	return ""
}

func (r *aoRows) ColumnTypeScanType(index int) reflect.Type {
	if st, ok := r.Rows.(driver.RowsColumnTypeScanType); ok {
		return st.ColumnTypeScanType(index)
	}
	return reflect.TypeOf("")
}

func (r *aoRows) ColumnTypeNullable(index int) (nullable, ok bool) {
	if n, ok := r.Rows.(driver.RowsColumnTypeNullable); ok {
		return n.ColumnTypeNullable(index)
	}
	return true, false
}

func (r *aoRows) ColumnTypeLength(index int) (length int64, ok bool) {
	if l, ok := r.Rows.(driver.RowsColumnTypeLength); ok {
		return l.ColumnTypeLength(index)
	}
	return 0, false
}

func (r *aoRows) ColumnTypePrecisionScale(index int) (precision, scale int64, ok bool) {
	if p, ok := r.Rows.(driver.RowsColumnTypePrecisionScale); ok {
		return p.ColumnTypePrecisionScale(index)
	}
	return 0, 0, false
}
