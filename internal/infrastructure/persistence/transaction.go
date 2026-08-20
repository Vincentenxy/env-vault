package persistence

import (
	"context"

	"gorm.io/gorm"
)

// txKey 用于在跨仓储调用时透传同一个 GORM 事务句柄。
type txKey struct{}

// WithTx 将事务句柄放入上下文，供同一用例涉及的多个仓储复用。
func WithTx(ctx context.Context, tx *gorm.DB) context.Context {
	return context.WithValue(ctx, txKey{}, tx)
}

// TxDB 返回上下文中的事务句柄；没有事务时返回默认数据库连接。
func TxDB(ctx context.Context, db *gorm.DB) *gorm.DB {
	if tx, ok := ctx.Value(txKey{}).(*gorm.DB); ok && tx != nil {
		return tx
	}
	return db
}
