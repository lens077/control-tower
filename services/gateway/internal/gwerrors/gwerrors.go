// Package gwerrors 统一网关自身产生的错误响应（404/405/无节点/超时/鉴权失败等非业务错误）。
//
// 契约（沿旧网关，前端已依赖）：
//   - 响应体是 Connect 规范错误 JSON：{code, message, details[]}；
//   - details 必须非空且 type/value 有效——connect-web 的 errorFromJson 会静默丢弃空 detail（历史坑）；
//   - 额外携带 X-Error-Reason 头供日志与前端排障。
//
// 实现：优先用 connect.NewErrorWriter 按请求协议（Connect/gRPC/gRPC-Web）写规范响应；
// 对非 RPC 形态的请求（如浏览器直捅未知路径）回退为手写 Connect 风格 JSON。
package gwerrors

import (
	"encoding/json"
	"net/http"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/encoding/protojson"
)

// HeaderReason 是错误原因头名（与旧网关一致）。
const HeaderReason = "X-Error-Reason"

// Domain 是 ErrorInfo.domain 的固定取值，标识错误出自网关而非后端。
const Domain = "gateway.control-tower"

// Writer 负责写出协议正确的错误响应。零值不可用，必须经 NewWriter 构造。
type Writer struct {
	ew *connect.ErrorWriter
}

// NewWriter 构造错误写出器。
func NewWriter() *Writer {
	return &Writer{ew: connect.NewErrorWriter()}
}

// Write 写出一个网关错误。reason 用 SCREAMING_SNAKE_CASE（如 ROUTE_NOT_FOUND）。
func (w *Writer) Write(rw http.ResponseWriter, r *http.Request, code connect.Code, reason, message string) {
	rw.Header().Set(HeaderReason, reason)

	info := &errdetails.ErrorInfo{
		Reason: reason,
		Domain: Domain,
	}

	if w.ew.IsSupported(r) {
		err := connect.NewError(code, connectMessageError(message))
		if detail, derr := connect.NewErrorDetail(info); derr == nil {
			err.AddDetail(detail)
		}
		_ = w.ew.Write(rw, r, err)
		return
	}

	// 非 RPC 请求的回退：Connect 风格 JSON + 等价 HTTP 状态码。
	writeFallbackJSON(rw, code, message, info)
}

type stringError string

func (e stringError) Error() string { return string(e) }

func connectMessageError(message string) error { return stringError(message) }

// writeFallbackJSON 按 Connect unary 错误体形状手写 JSON。
func writeFallbackJSON(rw http.ResponseWriter, code connect.Code, message string, info proto.Message) {
	type detailJSON struct {
		Type  string          `json:"type"`
		Value json.RawMessage `json:"debug,omitempty"`
	}
	// Connect JSON detail 的规范形态是 {type, value(base64), debug(json)}；
	// 回退路径只面向人读，附 debug 即可，type 保持有效非空。
	dbg, _ := protojson.Marshal(info)
	body := struct {
		Code    string       `json:"code"`
		Message string       `json:"message"`
		Details []detailJSON `json:"details"`
	}{
		Code:    code.String(),
		Message: message,
		Details: []detailJSON{{Type: string(proto.MessageName(info)), Value: dbg}},
	}
	buf, err := json.Marshal(body)
	if err != nil {
		http.Error(rw, message, httpStatus(code))
		return
	}
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(httpStatus(code))
	_, _ = rw.Write(buf)
}

// httpStatus 按 Connect 协议规范映射 HTTP 状态码。
func httpStatus(code connect.Code) int {
	switch code {
	case connect.CodeCanceled:
		return 499
	case connect.CodeUnknown:
		return 500
	case connect.CodeInvalidArgument:
		return 400
	case connect.CodeDeadlineExceeded:
		// 与 connect-go ErrorWriter 的实际映射一致（实测 504，非旧 spec 记忆中的 408）。
		return 504
	case connect.CodeNotFound:
		return 404
	case connect.CodeAlreadyExists:
		return 409
	case connect.CodePermissionDenied:
		return 403
	case connect.CodeResourceExhausted:
		return 429
	case connect.CodeFailedPrecondition:
		return 412
	case connect.CodeAborted:
		return 409
	case connect.CodeOutOfRange:
		return 400
	case connect.CodeUnimplemented:
		return 501
	case connect.CodeInternal:
		return 500
	case connect.CodeUnavailable:
		return 503
	case connect.CodeDataLoss:
		return 500
	case connect.CodeUnauthenticated:
		return 401
	default:
		return 500
	}
}
