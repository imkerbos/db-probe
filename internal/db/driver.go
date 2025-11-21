// Package db 提供数据库驱动抽象层
// 定义了统一的数据库驱动接口，支持 MySQL、TiDB 和 Oracle
// 每种数据库类型都有对应的驱动实现，提供驱动名称和默认探测 SQL
package db

import (
	"fmt"
)

// ProberDriver 数据库驱动接口
type ProberDriver interface {
	// DriverName 返回数据库驱动名称（用于 sql.Open）
	DriverName() string
	// DefaultQuery 返回默认的探测 SQL
	DefaultQuery() string
}

// MySQLDriver MySQL/TiDB 驱动实现
type MySQLDriver struct{}

func (d *MySQLDriver) DriverName() string {
	return "mysql"
}

func (d *MySQLDriver) DefaultQuery() string {
	return "SELECT 1"
}

// OracleDriver Oracle 驱动实现
type OracleDriver struct{}

func (d *OracleDriver) DriverName() string {
	return "godror"
}

func (d *OracleDriver) DefaultQuery() string {
	return "SELECT 1 FROM dual"
}

// GetDriver 根据数据库类型获取驱动
func GetDriver(dbType string) (ProberDriver, error) {
	switch dbType {
	case "mysql", "tidb":
		return &MySQLDriver{}, nil
	case "oracle":
		return &OracleDriver{}, nil
	default:
		return nil, fmt.Errorf("不支持的数据库类型: %s (支持的类型: mysql, tidb, oracle)", dbType)
	}
}

