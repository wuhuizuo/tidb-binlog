package sql

import (
	"database/sql"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/juju/errors"
	// Importing ClickHouse sql driver.
	_ "github.com/kshvakov/clickhouse"
	"github.com/ngaut/log"
	tddl "github.com/pingcap/tidb/ddl"
	"github.com/pingcap/tidb/infoschema"
	tmysql "github.com/pingcap/tidb/mysql"
	"github.com/pingcap/tidb/terror"
)

var (
	// MaxDMLRetryCount defines maximum number of times of DML retrying.
	MaxDMLRetryCount = 100
	// MaxDDLRetryCount defines maximum number of times of DDL retrying.
	MaxDDLRetryCount = 5
	// RetryWaitTime defines wait time when retrying.
	RetryWaitTime = 3 * time.Second
)

// ExecuteSQLs execute sqls in a transaction with retry.
func ExecuteSQLs(db *sql.DB, sqls []string, args [][]interface{}, isDDL bool) error {
	if len(sqls) == 0 {
		return nil
	}

	retryCount := MaxDMLRetryCount
	if isDDL {
		retryCount = MaxDDLRetryCount
	}

	var err error
	for i := 0; i < retryCount; i++ {
		if i > 0 {
			//log.Warnf("exec sql retry %d - %v", i, sqls)
			time.Sleep(RetryWaitTime)
		}

		err = ExecuteTxn(db, sqls, args)
		if err == nil {
			return nil
		}
	}

	return errors.Trace(err)
}

// ExecuteTxn executes transaction
func ExecuteTxn(db *sql.DB, sqls []string, args [][]interface{}) error {
	txn, err := db.Begin()
	if err != nil {
		log.Errorf("exec sqls[%v] begin failed %v", sqls, errors.ErrorStack(err))
		return errors.Trace(err)
	}

	for i := range sqls {
		//log.Debugf("[exec][sql]%s[args]%v", sqls[i], args[i])

		_, err = txn.Exec(sqls[i], args[i]...)
		if err != nil {
			log.Errorf("[exec][sql]%s[args]%v[error]%v", sqls[i], args[i], err)
			rerr := txn.Rollback()
			if rerr != nil {
				log.Errorf("[rollback][error]%v", rerr)
			}
			return errors.Trace(err)
		}
	}

	err = txn.Commit()
	if err != nil {
		log.Errorf("exec sqls[%v] commit failed %v", sqls, errors.ErrorStack(err))
		return errors.Trace(err)
	}

	return nil
}

// OpenDB creates an instance of sql.DB.
func OpenDB(proto string, host string, port int, username string, password string) (*sql.DB, error) {
	dbDSN := fmt.Sprintf("%s:%s@tcp(%s:%d)/?charset=utf8mb4,utf8&multiStatements=true", username, password, host, port)
	db, err := sql.Open(proto, dbDSN)
	if err != nil {
		return nil, errors.Trace(err)
	}

	return db, nil
}

// IgnoreDDLError checks the error can be ignored or not.
func IgnoreDDLError(err error) bool {
	mysqlErr, ok := errors.Cause(err).(*mysql.MySQLError)
	if !ok {
		return false
	}

	errCode := terror.ErrCode(mysqlErr.Number)
	// we can get error code from:
	// infoschema's error definition: https://github.com/pingcap/tidb/blob/master/infoschema/infoschema.go
	// DDL's error definition: https://github.com/pingcap/tidb/blob/master/ddl/ddl.go
	// tidb/mysql error code definition: https://github.com/pingcap/tidb/blob/master/mysql/errcode.go
	switch errCode {
	case infoschema.ErrDatabaseExists.Code(), infoschema.ErrDatabaseNotExists.Code(), infoschema.ErrDatabaseDropExists.Code(),
		infoschema.ErrTableExists.Code(), infoschema.ErrTableNotExists.Code(), infoschema.ErrTableDropExists.Code(),
		infoschema.ErrColumnExists.Code(), infoschema.ErrColumnNotExists.Code(), infoschema.ErrIndexExists.Code(),
		tddl.ErrCantDropFieldOrKey.Code(), tmysql.ErrDupKeyName:
		return true
	default:
		return false
	}
}

// CHHostAndPort is a CH host:port pair.
type CHHostAndPort struct {
	Host string
	Port int
}

// ParseCHAddr parses an address config string to CHHostAndPort pairs.
func ParseCHAddr(addr string) ([]CHHostAndPort, error) {
	hostParts := strings.Split(addr, ",")
	result := make([]CHHostAndPort, 0, len(hostParts))
	for _, hostStr := range hostParts {
		trimedHostStr := strings.TrimSpace(hostStr)
		host, portStr, err := net.SplitHostPort(trimedHostStr)
		if err != nil {
			return nil, errors.Trace(err)
		}
		port, err := strconv.Atoi(strings.TrimSpace(portStr))
		if err != nil {
			return nil, errors.Annotate(err, "port is not number")
		}
		result = append(result, CHHostAndPort{
			Host: host,
			Port: port,
		})
	}
	return result, nil
}

// OpenCH opens a connection to ClickHouse.
func OpenCH(proto string, host string, port int, username string, password string) (*sql.DB, error) {
	dbDSN := fmt.Sprintf("tcp://%s:%d", host, port)
	log.Infof("Connecting to %s", dbDSN)
	db, err := sql.Open(proto, dbDSN)
	if err != nil {
		return nil, errors.Trace(err)
	}

	return db, nil
}