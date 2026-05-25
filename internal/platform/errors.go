package platform

import "github.com/jackc/pgx/v5/pgconn"

// IsDuplicateKey returns true if err is a Postgres unique-violation (SQLSTATE 23505).
func IsDuplicateKey(err error) bool {
	for err != nil {
		if pe, ok := err.(*pgconn.PgError); ok {
			return pe.Code == "23505"
		}
		type unwrapper interface{ Unwrap() error }
		if u, ok := err.(unwrapper); ok {
			err = u.Unwrap()
		} else {
			break
		}
	}
	return false
}
