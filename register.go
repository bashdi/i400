package i400

import "database/sql"

var driverInstance = &Driver{}

func init() {
	sql.Register("as400", driverInstance)
}
