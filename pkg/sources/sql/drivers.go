/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package sqlsource

// This file exists solely to register the database/sql drivers the SQL
// source supports. Keeping the imports in their own file makes the dependency
// surface explicit and trivial to audit; adding or removing a driver is a
// single-file change.
//
// Both drivers register themselves in their package init blocks, so the
// blank imports are sufficient to make them discoverable via sql.Open.

import (
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
)
