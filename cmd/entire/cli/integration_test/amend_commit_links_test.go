//go:build integration

package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/execx"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
)

// A replacement-message amend may remove the old trailer. The contract is
// explicit recovery by checkpoint ID; post-rewrite does not add reverse links.
func TestAmendWithoutTrailer_ExplicitCheckpointRecovery(t *testing.T) {
	t.Parallel()
	ForEachBackend(t, func(t *testing.T, backend string) {
		env := NewFeatureBranchEnv(t)
		env.CheckpointStore = backend
		sess := env.NewSession()
		require.NoError(t, env.SimulateUserPromptSubmitWithPromptAndTranscriptPath(sess.ID, "write feature", sess.TranscriptPath))
		env.WriteFile("feature.txt", "agent work\n")
		sess.CreateTranscript("write feature", []FileChange{{Path: "feature.txt", Content: "agent work\n"}})
		require.NoError(t, env.SimulateStop(sess.ID, sess.TranscriptPath))
		env.GitCommitWithHooks("feature", "feature.txt")
		checkpointID := env.LatestCheckpointID()
		before := env.RunCLI("checkpoint", "explain", "--checkpoint", checkpointID, "--raw-transcript")

		// Pin real Git hooks to the shared test binary. Git supplies the actual
		// amend source and post-rewrite pair.
		binary := "'" + strings.ReplaceAll(filepath.ToSlash(getTestBinary()), "'", "'\\''") + "'"
		for _, hook := range []string{"prepare-commit-msg", "post-commit", "post-rewrite"} {
			name := filepath.Join(".git", "hooks", hook)
			env.WriteFile(name, "#!/bin/sh\nexec "+binary+" hooks git "+hook+" \"$@\"\n")
			require.NoError(t, os.Chmod(filepath.Join(env.RepoDir, name), 0o755))
		}
		cmd := execx.NonInteractive(t.Context(), "git", "commit", "--amend", "-m", "replacement message")
		cmd.Dir = env.RepoDir
		cmd.Env = env.gitHookEnv()
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", out)
		head := env.GetHeadHash()
		require.Empty(t, env.GetCheckpointIDFromCommitMessage(head))
		var summary checkpoint.CheckpointSummary
		ref, path := "entire/checkpoints/v1", CheckpointSummaryPath(checkpointID)
		if env.usingGitRefs() {
			ref, path = checkpointRefName(checkpointID), "metadata.json"
		}
		require.NoError(t, json.Unmarshal([]byte(testutil.RunGit(t, env.RepoDir, "show", ref+":"+path)), &summary))
		require.Empty(t, summary.LinkedCommits, "amend does not create reverse commit associations")
		require.Equal(t, before, env.RunCLI("checkpoint", "explain", "--checkpoint", checkpointID, "--raw-transcript"), "checkpoint ID remains the recovery path")
		require.NotContains(t, env.RunCLI("checkpoint", "explain", "--commit", head), checkpointID)
	})
}
