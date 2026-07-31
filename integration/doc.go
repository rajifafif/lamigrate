// Package integration provides isolated MySQL integration tests for lamigrate.
//
// These tests require a live MySQL server. Set the LAMIGRATE_TEST_MYSQL_DSN
// environment variable to point at a server with root privileges. Tests
// automatically create and drop temporary databases using the lamigrate_test_
// prefix so no real data is touched.
//
// Run with:
//
//	go test -tags=integration -count=1 -v ./integration/ -timeout 60s
package integration
