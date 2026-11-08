// Package sqlfiles embeds the SQL models and data quality tests so the
// transform binary carries exactly the SQL it was built with.
package sqlfiles

import "embed"

// FS holds silver/*.sql, gold/*.sql and tests/*.sql.
//
//go:embed silver/*.sql gold/*.sql tests/*.sql
var FS embed.FS
