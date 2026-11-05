package internal

// PostgreSQL keeps the same database/sql contract as SQLite. The small
// dialect adapter handles bind markers and the three SQLite insert forms;
// storage, authorization and synchronization remain shared Go code.
import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
)

type postgresDriver struct{}
type postgresConn struct{ driver.Conn }
type postgresStmt struct{ driver.Stmt }

func init() { sql.Register("grpvn-postgres", postgresDriver{}) }
func (postgresDriver) Open(dsn string) (driver.Conn, error) {
	c, err := (&pq.Driver{}).Open(dsn)
	if err != nil {
		return nil, err
	}
	return &postgresConn{c}, nil
}
func (c *postgresConn) Prepare(q string) (driver.Stmt, error) {
	s, err := c.Conn.Prepare(postgresSQL(q))
	if err != nil {
		return nil, err
	}
	return &postgresStmt{s}, nil
}
func (c *postgresConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}
func (c *postgresConn) Ping(ctx context.Context) error { return c.Conn.(driver.Pinger).Ping(ctx) }
func postgresValues(v []driver.NamedValue) []driver.NamedValue {
	for i := range v {
		if b, ok := v[i].Value.(bool); ok {
			if b {
				v[i].Value = int64(1)
			} else {
				v[i].Value = int64(0)
			}
		}
	}
	return v
}
func (c *postgresConn) ExecContext(ctx context.Context, q string, v []driver.NamedValue) (driver.Result, error) {
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, postgresSQL(q), postgresValues(v))
}
func (c *postgresConn) QueryContext(ctx context.Context, q string, v []driver.NamedValue) (driver.Rows, error) {
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, postgresSQL(q), postgresValues(v))
}
func (s *postgresStmt) Exec(v []driver.Value) (driver.Result, error) {
	for i, b := range v {
		if x, ok := b.(bool); ok {
			v[i] = int64(0)
			if x {
				v[i] = int64(1)
			}
		}
	}
	return s.Stmt.Exec(v)
}
func (s *postgresStmt) Query(v []driver.Value) (driver.Rows, error) {
	for i, b := range v {
		if x, ok := b.(bool); ok {
			v[i] = int64(0)
			if x {
				v[i] = int64(1)
			}
		}
	}
	return s.Stmt.Query(v)
}

var scalarMax = regexp.MustCompile(`(?i)\bMAX\(([^(),]+),([^()]+)\)`)
var pragmas = regexp.MustCompile(`(?m)^PRAGMA[^;]+;`)
var sqlInteger = regexp.MustCompile(`\bINTEGER\b`)

func postgresSQL(q string) string {
	q = scalarMax.ReplaceAllString(q, "GREATEST($1,$2)")
	q = strings.ReplaceAll(q, "(? AND", "(?=1 AND")
	trim := strings.TrimSpace(q)
	if strings.HasPrefix(strings.ToUpper(trim), "INSERT OR IGNORE INTO ") {
		q = strings.Replace(q, "INSERT OR IGNORE INTO", "INSERT INTO", 1)
		q = strings.TrimSuffix(strings.TrimSpace(q), ";") + " ON CONFLICT DO NOTHING"
	}
	var out strings.Builder
	n := 0
	quoted := byte(0)
	comment := false
	for i := 0; i < len(q); i++ {
		ch := q[i]
		if comment {
			out.WriteByte(ch)
			if ch == '\n' {
				comment = false
			}
			continue
		}
		if quoted != 0 {
			out.WriteByte(ch)
			if ch == quoted {
				if i+1 < len(q) && q[i+1] == quoted {
					i++
					out.WriteByte(q[i])
				} else {
					quoted = 0
				}
			}
			continue
		}
		if ch == '-' && i+1 < len(q) && q[i+1] == '-' {
			comment = true
			out.WriteByte(ch)
			continue
		}
		if ch == '\'' || ch == '"' {
			quoted = ch
			out.WriteByte(ch)
			continue
		}
		if ch == '?' {
			n++
			out.WriteString("$" + strconv.Itoa(n))
		} else {
			out.WriteByte(ch)
		}
	}
	return out.String()
}
func isPostgres(db *sql.DB) bool { _, ok := db.Driver().(postgresDriver); return ok }

// OpenDatabase accepts an explicit PostgreSQL URL or SQLite path. An empty
// value preserves the existing zero-configuration SQLite location.
func OpenDatabase(location string) (*sql.DB, error) {
	if !strings.HasPrefix(location, "postgres://") && !strings.HasPrefix(location, "postgresql://") {
		return openSQLite(location)
	}
	db, err := sql.Open("grpvn-postgres", location)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(30 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err = db.PingContext(ctx); err == nil {
		err = migratePostgres(db)
	}
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("open PostgreSQL: %w", err)
	}
	return db, nil
}

func migratePostgres(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// A transaction-scoped lock makes concurrent server/daemon startups safe.
	if _, err = tx.Exec("SELECT pg_advisory_xact_lock(717816534)"); err != nil {
		return err
	}
	schema := pragmas.ReplaceAllString(Schema, "")
	schema = strings.ReplaceAll(schema, "INTEGER PRIMARY KEY AUTOINCREMENT", "BIGINT PRIMARY KEY")
	schema = sqlInteger.ReplaceAllString(schema, "BIGINT")
	schema = strings.ReplaceAll(schema, "BLOB", "BYTEA")
	schema = strings.Replace(schema, "INSERT OR IGNORE INTO oauth_credentials", "INSERT INTO oauth_credentials", 1)
	schema = strings.Replace(schema, "a.credential_hash=p.credential_hash;", "a.credential_hash=p.credential_hash ON CONFLICT DO NOTHING;", 1)
	if _, err = tx.Exec(schema); err != nil {
		return fmt.Errorf("apply PostgreSQL schema: %w", err)
	}
	// A transactional row counter, rather than a sequence, serializes message
	// append commits. Thus seq>N cursors cannot skip an earlier pending commit.
	_, err = tx.Exec(`CREATE OR REPLACE FUNCTION instr(bytea,text) RETURNS integer LANGUAGE sql IMMUTABLE STRICT AS $$ SELECT POSITION(convert_to($2,'UTF8') IN $1) $$;
CREATE TABLE IF NOT EXISTS message_sequence(singleton BIGINT PRIMARY KEY CHECK(singleton=1),position BIGINT NOT NULL);
INSERT INTO message_sequence(singleton,position) SELECT 1,COALESCE(MAX(seq),0) FROM messages ON CONFLICT DO NOTHING;
CREATE OR REPLACE FUNCTION grpvn_message_sequence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 UPDATE message_sequence SET position=GREATEST(position+1,COALESCE(NEW.seq,0)) WHERE singleton=1 RETURNING position INTO NEW.seq;
 PERFORM pg_notify('grpvn_messages','changed');
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS grpvn_message_sequence ON messages;
CREATE TRIGGER grpvn_message_sequence BEFORE INSERT ON messages FOR EACH ROW EXECUTE FUNCTION grpvn_message_sequence();`)
	if err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO schema_version(version,applied_at) VALUES(?,?) ON CONFLICT DO NOTHING", CurrentSchemaVersion, time.Now().UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}

func databaseVersion(ctx context.Context, db *sql.DB) (int64, error) {
	var version int64
	query := "PRAGMA data_version"
	if isPostgres(db) {
		query = "SELECT COALESCE(MAX(seq),0) FROM messages"
	}
	err := db.QueryRowContext(ctx, query).Scan(&version)
	return version, err
}

func postgresLogPath() (string, error) {
	path, err := dbPath()
	if err != nil {
		return "", err
	}
	return path + ".postgres.daemon.log", nil
}

func configuredDatabase() string { return strings.TrimSpace(os.Getenv("GRPVN_DATABASE_URL")) }
