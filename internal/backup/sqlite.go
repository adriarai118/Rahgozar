package backup

// A tiny read-only SQLite reader: enough to pull rows out of another app's
// backup database (table B-trees, records, overflow pages). No writes, no
// indexes, no WAL.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
)

var errSQLite = errors.New("فایل پایگاه‌داده SQLite معتبر نیست")

type sqliteDB struct {
	b      []byte
	page   int // page size
	usable int
}

func openSQLite(b []byte) (*sqliteDB, error) {
	if len(b) < 100 || string(b[:16]) != "SQLite format 3\x00" {
		return nil, errSQLite
	}
	ps := int(binary.BigEndian.Uint16(b[16:18]))
	if ps == 1 {
		ps = 65536
	}
	if ps < 512 || ps&(ps-1) != 0 {
		return nil, errSQLite
	}
	return &sqliteDB{b: b, page: ps, usable: ps - int(b[20])}, nil
}

func (d *sqliteDB) pageAt(n uint32) ([]byte, error) {
	off := int64(n-1) * int64(d.page)
	if n == 0 || off+int64(d.page) > int64(len(d.b)) {
		return nil, fmt.Errorf("sqlite: page %d out of range", n)
	}
	return d.b[off : off+int64(d.page)], nil
}

func varint(b []byte) (uint64, int) {
	var v uint64
	for i := 0; i < 9 && i < len(b); i++ {
		if i == 8 {
			return v<<8 | uint64(b[i]), 9
		}
		v = v<<7 | uint64(b[i]&0x7f)
		if b[i] < 0x80 {
			return v, i + 1
		}
	}
	return v, len(b)
}

// scan calls fn with (rowid, payload) for every row of the table B-tree.
func (d *sqliteDB) scan(root uint32, fn func(rowid int64, payload []byte) error) error {
	seen := map[uint32]bool{}
	var walk func(n uint32) error
	walk = func(n uint32) error {
		if seen[n] {
			return errors.New("sqlite: loop in b-tree")
		}
		seen[n] = true
		pg, err := d.pageAt(n)
		if err != nil {
			return err
		}
		h := 0
		if n == 1 {
			h = 100
		}
		typ := pg[h]
		cells := int(binary.BigEndian.Uint16(pg[h+3 : h+5]))
		switch typ {
		case 0x05: // interior table
			ptrs := pg[h+12:]
			for i := 0; i < cells; i++ {
				c := int(binary.BigEndian.Uint16(ptrs[2*i:]))
				if c+4 > len(pg) {
					return errSQLite
				}
				if err := walk(binary.BigEndian.Uint32(pg[c:])); err != nil {
					return err
				}
			}
			return walk(binary.BigEndian.Uint32(pg[h+8:]))
		case 0x0d: // leaf table
			ptrs := pg[h+8:]
			for i := 0; i < cells; i++ {
				c := int(binary.BigEndian.Uint16(ptrs[2*i:]))
				if c >= len(pg) {
					return errSQLite
				}
				size, k := varint(pg[c:])
				c += k
				rowid, k := varint(pg[c:])
				c += k
				payload, err := d.payload(pg, c, int(size))
				if err != nil {
					return err
				}
				if err := fn(int64(rowid), payload); err != nil {
					return err
				}
			}
			return nil
		}
		return fmt.Errorf("sqlite: unexpected page type %#x", typ)
	}
	return walk(root)
}

// payload returns a cell's full payload, following overflow pages.
func (d *sqliteDB) payload(pg []byte, at, size int) ([]byte, error) {
	u := d.usable
	x := u - 35
	local := size
	if size > x {
		m := (u-12)*32/255 - 23
		k := m + (size-m)%(u-4)
		if k <= x {
			local = k
		} else {
			local = m
		}
	}
	if at+local > len(pg) {
		return nil, errSQLite
	}
	out := make([]byte, 0, size)
	out = append(out, pg[at:at+local]...)
	if local == size {
		return out, nil
	}
	if at+local+4 > len(pg) {
		return nil, errSQLite
	}
	next := binary.BigEndian.Uint32(pg[at+local:])
	for hops := 0; next != 0 && len(out) < size; hops++ {
		if hops > 1<<20 {
			return nil, errSQLite
		}
		op, err := d.pageAt(next)
		if err != nil {
			return nil, err
		}
		next = binary.BigEndian.Uint32(op)
		n := min(size-len(out), u-4)
		out = append(out, op[4:4+n]...)
	}
	if len(out) != size {
		return nil, errSQLite
	}
	return out, nil
}

// record decodes a row payload into Go values (nil, int64, float64, string, []byte).
func record(p []byte) ([]any, error) {
	hs, n := varint(p)
	if int(hs) > len(p) || n == 0 {
		return nil, errSQLite
	}
	var types []uint64
	for i := n; i < int(hs); {
		t, k := varint(p[i:])
		if k == 0 {
			return nil, errSQLite
		}
		types = append(types, t)
		i += k
	}
	body := p[hs:]
	vals := make([]any, len(types))
	for i, t := range types {
		var w int
		switch {
		case t == 0, t == 8, t == 9:
			w = 0
		case t >= 1 && t <= 4:
			w = int(t)
		case t == 5:
			w = 6
		case t == 6, t == 7:
			w = 8
		case t >= 12:
			w = int(t-12) / 2
		default:
			return nil, errSQLite
		}
		if w > len(body) {
			return nil, errSQLite
		}
		v := body[:w]
		body = body[w:]
		switch {
		case t == 0:
			vals[i] = nil
		case t == 8:
			vals[i] = int64(0)
		case t == 9:
			vals[i] = int64(1)
		case t >= 1 && t <= 6:
			var x int64
			if v[0]&0x80 != 0 {
				x = -1
			}
			for _, c := range v {
				x = x<<8 | int64(c)
			}
			vals[i] = x
		case t == 7:
			vals[i] = math.Float64frombits(binary.BigEndian.Uint64(v))
		case t%2 == 0:
			vals[i] = append([]byte(nil), v...)
		default:
			vals[i] = string(v)
		}
	}
	return vals, nil
}

// table reads all rows of a table as column-name maps.
func (d *sqliteDB) table(name string) ([]map[string]any, error) {
	var root uint32
	var sql string
	err := d.scan(1, func(_ int64, p []byte) error {
		r, err := record(p)
		if err != nil || len(r) < 5 {
			return err
		}
		if t, _ := r[0].(string); t != "table" {
			return nil
		}
		if n, _ := r[1].(string); strings.EqualFold(n, name) {
			rp, _ := r[3].(int64)
			root = uint32(rp)
			sql, _ = r[4].(string)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if root == 0 {
		return nil, fmt.Errorf("sqlite: table %s not found", name)
	}
	cols, pk := columns(sql)
	var rows []map[string]any
	err = d.scan(root, func(rowid int64, p []byte) error {
		r, err := record(p)
		if err != nil {
			return err
		}
		m := map[string]any{}
		for i, c := range cols {
			if i < len(r) {
				m[c] = r[i]
			}
		}
		if pk != "" && m[pk] == nil {
			m[pk] = rowid // INTEGER PRIMARY KEY is stored as the rowid
		}
		rows = append(rows, m)
		return nil
	})
	return rows, err
}

// columns parses column names (lower-case) from a CREATE TABLE statement and
// reports the INTEGER PRIMARY KEY column, if any.
func columns(sql string) (cols []string, pk string) {
	i := strings.IndexByte(sql, '(')
	j := strings.LastIndexByte(sql, ')')
	if i < 0 || j <= i {
		return nil, ""
	}
	depth, start := 0, i+1
	var defs []string
	for k := i + 1; k < j; k++ {
		switch sql[k] {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				defs = append(defs, sql[start:k])
				start = k + 1
			}
		}
	}
	defs = append(defs, sql[start:j])
	for _, d := range defs {
		f := strings.Fields(strings.TrimSpace(d))
		if len(f) == 0 {
			continue
		}
		switch strings.ToUpper(f[0]) {
		case "PRIMARY", "UNIQUE", "FOREIGN", "CHECK", "CONSTRAINT":
			continue
		}
		name := strings.ToLower(strings.Trim(f[0], "`\"[]'"))
		cols = append(cols, name)
		up := strings.ToUpper(d)
		if len(f) > 1 && strings.HasPrefix(strings.ToUpper(f[1]), "INTEGER") && strings.Contains(up, "PRIMARY KEY") {
			pk = name
		}
	}
	return cols, pk
}
