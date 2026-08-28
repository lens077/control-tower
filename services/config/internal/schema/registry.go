package schema

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/lens077/control-tower/services/config/internal/biz"
	"github.com/pelletier/go-toml/v2"
	"github.com/santhosh-tekuri/jsonschema/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

const (
	modeEnvironment = "CONFIG_SCHEMA_MODE"
	modeEnforce     = "enforce"
	modeObserve     = "observe"
)

//go:embed schemas/*/bootstrap.schema.json schemas/ecommerce-source-revision.txt
var bundledSchemas embed.FS

// Registry 在启动时编译已登记的 Schema，并校验匹配的写入。
type Registry struct {
	schemas        map[string]*jsonschema.Schema
	mode           string
	sourceRevision string
	log            *zap.Logger
}

// Module 只通过领域校验 seam 暴露 Registry。
var Module = fx.Module("config-schema",
	fx.Provide(fx.Annotate(New, fx.As(new(biz.ContentValidator)))),
)

// New 加载内嵌 Schema 快照。CONFIG_SCHEMA_MODE=observe 会记录违规位置，
// 但仍允许写入，供故障期间紧急旁路。
func New(logger *zap.Logger) (*Registry, error) {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv(modeEnvironment)))
	if mode == "" {
		mode = modeEnforce
	}
	return newRegistry(bundledSchemas, mode, logger)
}

func newRegistry(schemaFS fs.FS, mode string, logger *zap.Logger) (*Registry, error) {
	if mode != modeEnforce && mode != modeObserve {
		return nil, fmt.Errorf("%s must be %q or %q", modeEnvironment, modeEnforce, modeObserve)
	}
	if logger == nil {
		logger = zap.NewNop()
	}

	revisionData, err := fs.ReadFile(schemaFS, "schemas/ecommerce-source-revision.txt")
	if err != nil {
		return nil, fmt.Errorf("read schema provenance: %w", err)
	}

	files, err := fs.Glob(schemaFS, "schemas/*/bootstrap.schema.json")
	if err != nil {
		return nil, fmt.Errorf("list bootstrap schemas: %w", err)
	}
	if len(files) == 0 {
		return nil, errors.New("no bootstrap schemas bundled")
	}
	sort.Strings(files)

	sourceRevision := strings.TrimSpace(string(revisionData))
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(sourceRevision) {
		return nil, errors.New("schema provenance must be a full git revision")
	}

	registry := &Registry{
		schemas:        make(map[string]*jsonschema.Schema, len(files)),
		mode:           mode,
		sourceRevision: sourceRevision,
		log:            logger.Named("ConfigSchema"),
	}
	for _, filename := range files {
		namespace := path.Base(path.Dir(filename))
		data, readErr := fs.ReadFile(schemaFS, filename)
		if readErr != nil {
			return nil, fmt.Errorf("read schema for %s: %w", namespace, readErr)
		}
		compiler := jsonschema.NewCompiler()
		resource := "inmemory://" + namespace + "/bootstrap.schema.json"
		if addErr := compiler.AddResource(resource, bytes.NewReader(data)); addErr != nil {
			return nil, fmt.Errorf("load schema for %s: %w", namespace, addErr)
		}
		compiled, compileErr := compiler.Compile(resource)
		if compileErr != nil {
			return nil, fmt.Errorf("compile schema for %s: %w", namespace, compileErr)
		}
		registry.schemas[namespace] = compiled
	}

	registry.log.Info("bootstrap schema validation enabled",
		zap.String("mode", mode),
		zap.Int("schemas", len(registry.schemas)),
		zap.String("ecommerce_revision", registry.sourceRevision),
	)
	return registry, nil
}

// Validate 放行未登记目标；已登记的 bootstrap 写入必须在持久化前通过校验。
// observe 模式只记录位置并放行。
func (r *Registry) Validate(target biz.ContentTarget, format biz.ConfigFormat, value string) error {
	compiled, ok := r.schemas[target.Namespace]
	if !ok || target.Key != "bootstrap.yaml" {
		return nil
	}

	document, err := normalize(format, value)
	if err == nil {
		err = compiled.Validate(document)
	}
	if err == nil {
		return nil
	}

	violation := newViolation(err)
	if r.mode == modeObserve {
		r.log.Warn("bootstrap schema violation allowed in observe mode",
			zap.String("namespace", target.Namespace),
			zap.String("environment", target.Environment),
			zap.String("key", target.Key),
			zap.Strings("locations", violation.Locations()),
			zap.String("ecommerce_revision", r.sourceRevision),
		)
		return nil
	}
	return violation
}

func normalize(format biz.ConfigFormat, value string) (any, error) {
	var document any
	switch format {
	case biz.FormatYAML:
		if err := yaml.Unmarshal([]byte(value), &document); err != nil {
			return nil, err
		}
	case biz.FormatJSON:
		if err := json.Unmarshal([]byte(value), &document); err != nil {
			return nil, err
		}
	case biz.FormatTOML:
		if err := toml.Unmarshal([]byte(value), &document); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported schema format")
	}

	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	var normalized any
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}
