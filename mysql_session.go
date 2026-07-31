package lamigrate

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/go-sql-driver/mysql"
)

// newPrivateSession creates a fresh one-session *sql.DB from the stored
// cloned configuration. It creates a new go-sql-driver/mysql connector,
// sets max open/idle connections to 1, obtains one *sql.Conn, and returns
// both the connection and the private pool. The caller MUST close the
// connection and pool after use via closeSession.
//
// Every call produces a completely independent session: new connector,
// new pool, new connection. The physical MySQL session is never shared
// across phases or reused by a later call. Architecture §10.
func (m *Migrator) newPrivateSession(ctx context.Context) (*sql.Conn, *sql.DB, error) {
	// Guard against nil config — NewMySQL should never produce this,
	// but defensive programming prevents a panic deep in the driver.
	if m.config == nil {
		return nil, nil, fmt.Errorf(
			"%w: stored mysql.Config is nil (Migrator not properly constructed)",
			ErrUnsupportedDriver,
		)
	}

	// Create a fresh connector from the stored cloned config.
	// mysql.NewConnector parses the DSN internally; this performs no I/O.
	connector, err := mysql.NewConnector(m.config)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"%w: cannot create MySQL connector from stored config: %v",
			ErrUnsupportedDriver, err,
		)
	}

	// Create a private pool backed by the fresh connector.
	// sql.OpenDB does not open a network connection.
	pool := sql.OpenDB(connector)
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)

	// Obtain one dedicated connection from the private pool.
	conn, err := pool.Conn(ctx)
	if err != nil {
		// Pool open but connection failed — close the pool before returning.
		_ = pool.Close()
		return nil, nil, fmt.Errorf(
			"%w: cannot obtain dedicated MySQL connection: %v",
			ErrUnsupportedDriver, err,
		)
	}

	return conn, pool, nil
}

// closeSession closes the dedicated connection and the private pool,
// forcing physical session termination. It is safe to call with nil
// values. Errors from both close operations are joined.
//
// After closeSession returns, the physical MySQL session is guaranteed
// to be terminated (assuming the driver's Close semantics hold).
func closeSession(conn *sql.Conn, pool *sql.DB) error {
	var firstErr error

	if conn != nil {
		if err := conn.Close(); err != nil {
			firstErr = fmt.Errorf("close dedicated connection: %w", err)
		}
	}

	if pool != nil {
		if err := pool.Close(); err != nil {
			if firstErr != nil {
				return fmt.Errorf("%w; close private pool: %v", firstErr, err)
			}
			firstErr = fmt.Errorf("close private pool: %w", err)
		}
	}

	return firstErr
}
