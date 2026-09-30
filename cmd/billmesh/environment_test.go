package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadEnvironmentLoadsDotEnvWithoutOverridingExistingValues(t *testing.T) {
	const (
		fromFileKey = "BILLMESH_DOTENV_TEST_FROM_FILE"
		existingKey = "BILLMESH_DOTENV_TEST_EXISTING"
	)
	unsetEnvironmentForTest(t, fromFileKey)
	t.Setenv(existingKey, "from-environment")

	path := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, os.WriteFile(path, []byte(
		fromFileKey+"=from-file\n"+
			existingKey+"=from-file\n",
	), 0o600))

	require.NoError(t, loadEnvironment(path))
	require.Equal(t, "from-file", os.Getenv(fromFileKey))
	require.Equal(t, "from-environment", os.Getenv(existingKey))
}

func TestLoadEnvironmentAllowsMissingFile(t *testing.T) {
	require.NoError(t, loadEnvironment(filepath.Join(t.TempDir(), ".env")))
}

func TestLoadEnvironmentRejectsMalformedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, os.WriteFile(path, []byte("VALUE='unterminated\n"), 0o600))

	require.ErrorContains(t, loadEnvironment(path), "load .env")
}

func unsetEnvironmentForTest(t *testing.T, key string) {
	t.Helper()
	old, existed := os.LookupEnv(key)
	require.NoError(t, os.Unsetenv(key))
	t.Cleanup(func() {
		if existed {
			require.NoError(t, os.Setenv(key, old))
			return
		}
		require.NoError(t, os.Unsetenv(key))
	})
}
