// Package multidb provides multi-database support for NornicDB.
//
// This package implements Neo4j 4.x-style multi-database support, allowing
// multiple logical databases (tenants) to share a single physical storage backend
// while maintaining complete data isolation.
package multidb

import (
	"errors"
)

// Multi-database error types
var (
	ErrDatabaseNotFound      = errors.New("database not found")
	ErrDatabaseExists        = errors.New("database already exists")
	ErrInvalidDatabaseName   = errors.New("invalid database name")
	ErrMaxDatabasesReached   = errors.New("maximum number of databases reached")
	ErrCannotDropSystemDB    = errors.New("cannot drop system database")
	ErrCannotDropDefaultDB   = errors.New("cannot drop default database")
	ErrDatabaseOffline       = errors.New("database is offline")
)

