package biz

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type fakeConfigRepo struct {
	putCount int
	revision *ConfigRevision
}

func (r *fakeConfigRepo) ListNamespaces(context.Context) ([]*NamespaceInfo, error) {
	return nil, nil
}

func (r *fakeConfigRepo) ListEntries(context.Context, string, string, string) ([]*ConfigEntry, error) {
	return nil, nil
}

func (r *fakeConfigRepo) GetEntry(context.Context, string, string, string) (*ConfigEntry, error) {
	return nil, ErrKeyNotFound
}

func (r *fakeConfigRepo) PutEntry(_ context.Context, in PutParams) (*ConfigEntry, error) {
	r.putCount++
	return &ConfigEntry{
		Namespace: in.Namespace, Environment: in.Environment, Key: in.Key,
		Format: in.Format, Value: in.Value, Version: int32(r.putCount),
	}, nil
}

func (r *fakeConfigRepo) DeleteEntry(context.Context, string, string, string) (bool, error) {
	return false, nil
}

func (r *fakeConfigRepo) ListRevisions(context.Context, string, string, string) ([]*ConfigRevision, error) {
	return nil, nil
}

func (r *fakeConfigRepo) GetRevision(context.Context, string, string, string, int32) (*ConfigRevision, error) {
	if r.revision == nil {
		return nil, ErrRevisionNotFound
	}
	return r.revision, nil
}

type fakeWatcher struct{}

func (fakeWatcher) Subscribe(string, string, []string) (<-chan ChangeEvent, func()) {
	changes := make(chan ChangeEvent)
	return changes, func() { close(changes) }
}

type fakeContentValidator struct {
	err       error
	callCount int
	target    ContentTarget
}

func (v *fakeContentValidator) Validate(target ContentTarget, _ ConfigFormat, _ string) error {
	v.callCount++
	v.target = target
	return v.err
}

func newTestUseCase(repo ConfigRepo, validator ContentValidator) *ConfigUseCase {
	return NewConfigUseCase(repo, fakeWatcher{}, validator, zap.NewNop())
}

func TestPutKeyRejectsSchemaViolationBeforePersistence(t *testing.T) {
	repo := &fakeConfigRepo{}
	validator := &fakeContentValidator{err: errors.New("invalid configuration at /search")}
	uc := newTestUseCase(repo, validator)

	_, err := uc.PutKey(context.Background(), PutParams{
		Namespace: "cart", Environment: "dev", Key: "bootstrap.yaml",
		Format: FormatYAML, Value: "search: {}\n",
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidFormat)
	assert.Equal(t, 0, repo.putCount)
	assert.Equal(t, 1, validator.callCount)
	assert.Equal(t, ContentTarget{Namespace: "cart", Environment: "dev", Key: "bootstrap.yaml"}, validator.target)
}

func TestPutKeyChecksSyntaxBeforeSchema(t *testing.T) {
	repo := &fakeConfigRepo{}
	validator := &fakeContentValidator{}
	uc := newTestUseCase(repo, validator)

	_, err := uc.PutKey(context.Background(), PutParams{
		Namespace: "cart", Environment: "dev", Key: "bootstrap.yaml",
		Format: FormatYAML, Value: "server: [\n",
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidFormat)
	assert.Equal(t, 0, validator.callCount)
	assert.Equal(t, 0, repo.putCount)
}

func TestPutKeyWithoutValidatorKeepsExistingBehavior(t *testing.T) {
	repo := &fakeConfigRepo{}
	uc := newTestUseCase(repo, nil)

	entry, err := uc.PutKey(context.Background(), PutParams{
		Namespace: "gateway", Environment: "dev", Key: "routes.yaml",
		Format: FormatYAML, Value: "routes: []\n",
	})

	require.NoError(t, err)
	assert.Equal(t, int32(1), entry.Version)
	assert.Equal(t, 1, repo.putCount)
}

func TestRollbackCannotBypassSchemaValidation(t *testing.T) {
	repo := &fakeConfigRepo{revision: &ConfigRevision{
		Version: 2, Format: FormatYAML, Value: "search: {}\n",
	}}
	validator := &fakeContentValidator{err: errors.New("invalid configuration at /search")}
	uc := newTestUseCase(repo, validator)

	_, err := uc.Rollback(context.Background(), "cart", "dev", "bootstrap.yaml", 2, "", "admin")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidFormat)
	assert.Equal(t, 1, validator.callCount)
	assert.Equal(t, 0, repo.putCount)
}

func TestRollbackPersistsSchemaValidRevision(t *testing.T) {
	repo := &fakeConfigRepo{revision: &ConfigRevision{
		Version: 2, Format: FormatYAML, Value: "server: {}\n",
	}}
	validator := &fakeContentValidator{}
	uc := newTestUseCase(repo, validator)

	entry, err := uc.Rollback(context.Background(), "cart", "dev", "bootstrap.yaml", 2, "", "admin")

	require.NoError(t, err)
	assert.Equal(t, "server: {}\n", entry.Value)
	assert.Equal(t, 1, validator.callCount)
	assert.Equal(t, 1, repo.putCount)
}
