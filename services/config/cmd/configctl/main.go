// configctl 是 Config Center 的命令行管理面：从本地文件写 key、读回校验、列举。
//
// 为什么存在：控制台适合人点，但「把仓库里的一份 YAML 放进某个 namespace/environment
// 的某个 key」这种操作要能在终端和 CI 里复现，且带 dry-run 与写后读回。
//
// 凭据只从环境变量或 --token-file 进来，绝不写进仓库（AGENTS.md 硬约束 4）：
//
//	CONFIG_CENTER_ENDPOINT        默认 https://config-api.apikv.com
//	CONFIG_CENTER_SERVICE_TOKEN   operator machine token（role=operator 才能写）
//	CONFIG_CENTER_BEARER_TOKEN    管理员 Casdoor JWT，二选一
//
// 用法：
//
//	configctl put  -namespace observability -environment prod \
//	               -key grafana/datasources/jaeger \
//	               -file examples/grafana-datasources/jaeger.yaml \
//	               -comment "接入 Jaeger 数据源" [-format yaml] [-dry-run]
//	configctl get  -namespace observability -environment prod -key grafana/datasources/jaeger
//	configctl ls   -namespace observability -environment prod [-prefix grafana/]
//	configctl namespaces
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	configv1 "github.com/lens077/control-tower/api/config/v1"
	"github.com/lens077/control-tower/sdk/configadmin"
)

const defaultEndpoint = "https://config-api.apikv.com"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "configctl: %v\n", err)
		os.Exit(1)
	}
}

// run 是可测试入口：参数、输出、错误都显式传进来，不碰全局状态。
func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		usage(stderr)
		return errors.New("a subcommand is required")
	}
	switch args[0] {
	case "put":
		return runPut(ctx, args[1:], stdout)
	case "get":
		return runGet(ctx, args[1:], stdout)
	case "ls", "list":
		return runList(ctx, args[1:], stdout)
	case "namespaces":
		return runNamespaces(ctx, args[1:], stdout)
	case "-h", "--help", "help":
		usage(stdout)
		return nil
	default:
		usage(stderr)
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `configctl —— Config Center 命令行管理面

  configctl put         从文件或 stdin 写 key（支持 -dry-run）
  configctl get         读 key 当前值
  configctl ls          列出 namespace×environment 下的 key
  configctl namespaces  列出已有 namespace 及其 environment

凭据（二选一，只从环境变量或 -token-file 读）：
  CONFIG_CENTER_SERVICE_TOKEN   operator machine token
  CONFIG_CENTER_BEARER_TOKEN    管理员 Casdoor JWT
端点：
  CONFIG_CENTER_ENDPOINT        默认 `+defaultEndpoint+`
`)
}

// commonFlags 是四个子命令共享的连接参数。
type commonFlags struct {
	endpoint   string
	tokenFile  string
	clientName string
}

func bindCommon(set *flag.FlagSet) *commonFlags {
	common := &commonFlags{}
	set.StringVar(&common.endpoint, "endpoint", envOr("CONFIG_CENTER_ENDPOINT", defaultEndpoint), "Config Center 端点")
	set.StringVar(&common.tokenFile, "token-file", "", "从文件读 machine token（优先于环境变量）")
	set.StringVar(&common.clientName, "client-name", "configctl", "x-config-center-client-name，用于审计与 /connections")
	return common
}

func (c *commonFlags) client() (*configadmin.Client, error) {
	credential := configadmin.Credential{
		MachineToken: os.Getenv("CONFIG_CENTER_SERVICE_TOKEN"),
		BearerToken:  os.Getenv("CONFIG_CENTER_BEARER_TOKEN"),
	}
	if c.tokenFile != "" {
		contents, err := os.ReadFile(c.tokenFile)
		if err != nil {
			return nil, fmt.Errorf("read token file: %w", err)
		}
		credential.MachineToken = strings.TrimSpace(string(contents))
	}
	client, err := configadmin.New(c.endpoint, credential, configadmin.WithClientName(c.clientName))
	if err != nil {
		return nil, fmt.Errorf("%w (设置 CONFIG_CENTER_SERVICE_TOKEN 或 CONFIG_CENTER_BEARER_TOKEN)", err)
	}
	return client, nil
}

func runPut(ctx context.Context, args []string, stdout io.Writer) error {
	set := flag.NewFlagSet("put", flag.ContinueOnError)
	common := bindCommon(set)
	namespace := set.String("namespace", "", "namespace，必填")
	environment := set.String("environment", "", "environment，必填")
	key := set.String("key", "", "key，必填")
	file := set.String("file", "", "value 来源文件；写 - 表示读 stdin")
	format := set.String("format", "", "yaml|toml|json|plaintext，留空时按文件后缀推断")
	comment := set.String("comment", "", "变更备注，进版本历史")
	description := set.String("description", "", "key 描述")
	secret := set.Bool("secret", false, "标记为 secret（列表里脱敏）")
	dryRun := set.Bool("dry-run", false, "只比对不写入")
	if err := set.Parse(args); err != nil {
		return err
	}

	value, sourceName, err := readValue(*file, os.Stdin)
	if err != nil {
		return err
	}
	resolvedFormat, err := resolveFormat(*format, sourceName)
	if err != nil {
		return err
	}

	client, err := common.client()
	if err != nil {
		return err
	}
	selector := configadmin.Selector{Namespace: *namespace, Environment: *environment, Key: *key}

	// 先读一次：区分「新建」与「更新」，并让 dry-run 能说清会不会真的改变什么。
	current, err := client.Get(ctx, selector)
	switch {
	case err == nil:
	case errors.Is(err, configadmin.ErrNotFound):
		current = configadmin.Entry{}
	default:
		return err
	}
	exists := current.Version > 0

	if *dryRun {
		return reportDryRun(stdout, selector, current, exists, value, resolvedFormat)
	}

	entry, err := client.Put(ctx, configadmin.PutRequest{
		Selector:    selector,
		Format:      resolvedFormat,
		Value:       value,
		Comment:     *comment,
		Description: *description,
		IsSecret:    *secret,
	})
	if err != nil {
		return err
	}

	action := "created"
	if exists {
		action = "updated"
	}
	fmt.Fprintf(stdout, "%s %s (format=%s version=%d bytes=%d)\n",
		action, entry.Selector, configadmin.FormatName(entry.Format), entry.Version, len(value))

	// 写后读回：服务端可能因 format 校验改写或拒绝，只看 PutKey 的回执不算验证。
	verified, err := client.Get(ctx, selector)
	if err != nil {
		return fmt.Errorf("read back after write: %w", err)
	}
	if verified.Value != value {
		return fmt.Errorf("read back mismatch for %s: server value differs from the file", selector)
	}
	fmt.Fprintf(stdout, "verified %s at version %d\n", selector, verified.Version)
	return nil
}

func reportDryRun(stdout io.Writer, selector configadmin.Selector, current configadmin.Entry, exists bool, value string, format configv1.ConfigFormat) error {
	if !exists {
		fmt.Fprintf(stdout, "dry-run: would create %s (format=%s bytes=%d)\n",
			selector, configadmin.FormatName(format), len(value))
		return nil
	}
	if current.Value == value && current.Format == format {
		fmt.Fprintf(stdout, "dry-run: %s already matches at version %d; nothing to do\n", selector, current.Version)
		return nil
	}
	fmt.Fprintf(stdout, "dry-run: would update %s from version %d (format %s -> %s, bytes %d -> %d)\n",
		selector, current.Version,
		configadmin.FormatName(current.Format), configadmin.FormatName(format),
		len(current.Value), len(value))
	return nil
}

func runGet(ctx context.Context, args []string, stdout io.Writer) error {
	set := flag.NewFlagSet("get", flag.ContinueOnError)
	common := bindCommon(set)
	namespace := set.String("namespace", "", "namespace，必填")
	environment := set.String("environment", "", "environment，必填")
	key := set.String("key", "", "key，必填")
	metaOnly := set.Bool("meta", false, "只打印元数据，不打印 value")
	if err := set.Parse(args); err != nil {
		return err
	}
	client, err := common.client()
	if err != nil {
		return err
	}
	entry, err := client.Get(ctx, configadmin.Selector{Namespace: *namespace, Environment: *environment, Key: *key})
	if err != nil {
		return err
	}
	if *metaOnly {
		fmt.Fprintf(stdout, "%s format=%s version=%d secret=%t updated_by=%s\n",
			entry.Selector, configadmin.FormatName(entry.Format), entry.Version, entry.IsSecret, entry.UpdatedBy)
		return nil
	}
	fmt.Fprint(stdout, entry.Value)
	if !strings.HasSuffix(entry.Value, "\n") {
		fmt.Fprintln(stdout)
	}
	return nil
}

func runList(ctx context.Context, args []string, stdout io.Writer) error {
	set := flag.NewFlagSet("ls", flag.ContinueOnError)
	common := bindCommon(set)
	namespace := set.String("namespace", "", "namespace，必填")
	environment := set.String("environment", "", "environment，必填")
	prefix := set.String("prefix", "", "按 key 前缀过滤")
	if err := set.Parse(args); err != nil {
		return err
	}
	client, err := common.client()
	if err != nil {
		return err
	}
	entries, err := client.ListKeys(ctx, *namespace, *environment, *prefix)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Fprintf(stdout, "no keys in %s/%s\n", *namespace, *environment)
		return nil
	}
	for _, entry := range entries {
		fmt.Fprintf(stdout, "%s\tformat=%s\tversion=%d\tsecret=%t\n",
			entry.Key, configadmin.FormatName(entry.Format), entry.Version, entry.IsSecret)
	}
	return nil
}

func runNamespaces(ctx context.Context, args []string, stdout io.Writer) error {
	set := flag.NewFlagSet("namespaces", flag.ContinueOnError)
	common := bindCommon(set)
	if err := set.Parse(args); err != nil {
		return err
	}
	client, err := common.client()
	if err != nil {
		return err
	}
	namespaces, err := client.ListNamespaces(ctx)
	if err != nil {
		return err
	}
	for _, item := range namespaces {
		fmt.Fprintf(stdout, "%s\t%s\n", item.Name, strings.Join(item.Environments, ","))
	}
	return nil
}

// readValue 读入待写内容。返回的 sourceName 用于按后缀推断格式；stdin 没有后缀。
func readValue(file string, stdin io.Reader) (string, string, error) {
	switch {
	case file == "":
		return "", "", errors.New("-file is required (use - for stdin)")
	case file == "-":
		contents, err := io.ReadAll(stdin)
		if err != nil {
			return "", "", fmt.Errorf("read stdin: %w", err)
		}
		return string(contents), "", nil
	default:
		contents, err := os.ReadFile(file)
		if err != nil {
			return "", "", fmt.Errorf("read %s: %w", file, err)
		}
		return string(contents), file, nil
	}
}

// resolveFormat 显式 -format 优先；否则按文件后缀推断。推不出来就报错，
// 不默默按 plaintext 写——那会绕过服务端的语法校验。
func resolveFormat(explicit, sourceName string) (configv1.ConfigFormat, error) {
	if strings.TrimSpace(explicit) != "" {
		return configadmin.ParseFormat(explicit)
	}
	if sourceName != "" {
		if format, ok := configadmin.FormatForPath(sourceName); ok {
			return format, nil
		}
	}
	return configv1.ConfigFormat_CONFIG_FORMAT_UNSPECIFIED,
		errors.New("-format is required when it cannot be inferred from the file extension")
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
