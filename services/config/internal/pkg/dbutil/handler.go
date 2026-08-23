package dbutil

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type Handler struct {
	ErrorMappings map[string]error
	NoRowsError   error
	NoRowsHandler func(err error) error
	EnableLogging bool
	Logger        func(err error, pgErr *pgconn.PgError)
}

type HandlerOption func(*Handler)

func WithErrorMapping(code string, bizErr error) HandlerOption {
	return func(h *Handler) {
		if h.ErrorMappings == nil {
			h.ErrorMappings = make(map[string]error)
		}
		h.ErrorMappings[code] = bizErr
	}
}

func WithLogging(enable bool) HandlerOption {
	return func(h *Handler) {
		h.EnableLogging = enable
	}
}

func WithLogger(logger func(err error, pgErr *pgconn.PgError)) HandlerOption {
	return func(h *Handler) {
		h.Logger = logger
	}
}

func WithNoRowsError(bizErr error) HandlerOption {
	return func(h *Handler) {
		h.NoRowsError = bizErr
	}
}

func WithNoRowsHandler(handler func(err error) error) HandlerOption {
	return func(h *Handler) {
		h.NoRowsHandler = handler
	}
}

func NewHandler(opts ...HandlerOption) *Handler {
	h := &Handler{
		ErrorMappings: make(map[string]error),
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

func (h *Handler) HandleError(err error, noRowsErr ...error) (error, bool) {
	if err == nil {
		return nil, false
	}

	if errors.Is(err, pgx.ErrNoRows) {
		if h.EnableLogging && h.Logger != nil {
			h.Logger(err, nil)
		}
		if len(noRowsErr) > 0 && noRowsErr[0] != nil {
			return noRowsErr[0], true
		}
		if h.NoRowsHandler != nil {
			return h.NoRowsHandler(err), true
		}
		if h.NoRowsError != nil {
			return h.NoRowsError, true
		}
		return err, false
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		if h.EnableLogging && h.Logger != nil {
			h.Logger(err, nil)
		}
		return err, false
	}

	if h.EnableLogging && h.Logger != nil {
		h.Logger(err, pgErr)
	}

	code := pgErr.Code

	if bizErr, ok := h.ErrorMappings[code]; ok {
		return bizErr, true
	}

	return err, false
}

func (h *Handler) WrapError(err error, wrapMsg string) error {
	wrappedErr, handled := h.HandleError(err)
	if handled {
		return wrappedErr
	}
	return fmt.Errorf("%s: %w", wrapMsg, err)
}

func (h *Handler) MustHandleError(err error, noRowsErr ...error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, pgx.ErrNoRows) {
		if h.EnableLogging && h.Logger != nil {
			h.Logger(err, nil)
		}
		if len(noRowsErr) > 0 && noRowsErr[0] != nil {
			return noRowsErr[0]
		}
		if h.NoRowsHandler != nil {
			return h.NoRowsHandler(err)
		}
		if h.NoRowsError != nil {
			return h.NoRowsError
		}
		return fmt.Errorf("not found")
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		if h.EnableLogging && h.Logger != nil {
			h.Logger(err, nil)
		}
		return err
	}

	if h.EnableLogging && h.Logger != nil {
		h.Logger(err, pgErr)
	}

	code := pgErr.Code

	if bizErr, ok := h.ErrorMappings[code]; ok {
		return bizErr
	}

	fmt.Println("code:", code)
	switch code {
	case "23505":
		return fmt.Errorf("唯一约束冲突: %s", pgErr.Detail)
	case "23503":
		return fmt.Errorf("外键约束冲突: %s", pgErr.Detail)
	case "23502":
		return fmt.Errorf("非空约束冲突: 列 %s 不能为空", pgErr.ColumnName)
	case "23514":
		return fmt.Errorf("检查约束冲突: %s", pgErr.ConstraintName)
	case "23001":
		return fmt.Errorf("限制约束冲突: %s", pgErr.ConstraintName)
	case "23P01":
		return fmt.Errorf("排他约束冲突: %s", pgErr.ConstraintName)
	case "23000", "IntegrityConstraintViolation":
		return fmt.Errorf("完整性约束冲突: %s", pgErr.Detail)
	case "40001":
		return fmt.Errorf("检测到TR死锁，请重试")
	case "55P03":
		return fmt.Errorf("锁不可用，请重试")
	case "54001":
		return fmt.Errorf("语句太复杂，超过程序限制")
	case "53300":
		return fmt.Errorf("连接数过多，请稍后重试")
	case "53100":
		return fmt.Errorf("磁盘空间不足")
	case "53200":
		return fmt.Errorf("内存不足")
	case "57014":
		return fmt.Errorf("查询被取消")
	case "40000":
		return fmt.Errorf("事务回滚: %s", pgErr.Message)
	case "08000", "08003", "08006", "08001", "08004", "InvalidTransactionState":
		return fmt.Errorf("无效的事务状态: %s", pgErr.Message)
	case "25006":
		return fmt.Errorf("只读事务中不能执行写操作")
	case "0A000":
		return fmt.Errorf("功能不支持: %s", pgErr.Message)
	case "42000", "SyntaxErrorOrAccessRuleViolation":
		return fmt.Errorf("语法错误或访问规则冲突: %s", pgErr.Message)
	case "42P01":
		return fmt.Errorf("表不存在: %s", pgErr.TableName)
	case "42703":
		return fmt.Errorf("列不存在: %s", pgErr.ColumnName)
	case "42883":
		return fmt.Errorf("函数不存在: %s", pgErr.Message)
	case "42P07":
		return fmt.Errorf("表已存在: %s", pgErr.TableName)
	case "42701":
		return fmt.Errorf("列已存在: %s", pgErr.ColumnName)
	case "42723":
		return fmt.Errorf("函数已存在: %s", pgErr.Message)
	case "42710":
		return fmt.Errorf("对象已存在: %s", pgErr.Message)
	case "24000":
		return fmt.Errorf("无效的游标状态: %s", pgErr.Message)
	case "3D000", "InvalidSchemaName", "InvalidCatalogName":
		return fmt.Errorf("无效的 schema 或 catalog 名称: %s", pgErr.SchemaName)
	case "22P02":
		return fmt.Errorf("无效的文本表示: %s", pgErr.Message)
	default:
		return fmt.Errorf("数据库错误 [%s]: %s", code, pgErr.Message)
	}
}
