package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

type projectAddOptions struct {
	path string
	id   string
	name string
}

// addProjectRequest mirrors the daemon's project AddInput body for
// POST /api/v1/projects. projectId and name are optional (pointers omit them).
type addProjectRequest struct {
	Path      string  `json:"path"`
	ProjectID *string `json:"projectId,omitempty"`
	Name      *string `json:"name,omitempty"`
}

type projectResult struct {
	Project struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	} `json:"project"`
}

func newProjectCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage projects",
	}
	cmd.AddCommand(newProjectAddCommand(ctx))
	return cmd
}

func newProjectAddCommand(ctx *commandContext) *cobra.Command {
	var opts projectAddOptions
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Register a local git repo as a project",
		Long: "Register a local git repo as a project so sessions can be spawned in it.\n\n" +
			"The path must be an existing git repository on disk.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.path == "" {
				return usageError{fmt.Errorf("--path is required")}
			}
			req := addProjectRequest{Path: opts.path}
			if opts.id != "" {
				req.ProjectID = &opts.id
			}
			if opts.name != "" {
				req.Name = &opts.name
			}
			var res projectResult
			if err := ctx.postJSON(cmd.Context(), "projects", req, &res); err != nil {
				return err
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "registered project %s at %s\n", res.Project.ID, res.Project.Path)
			return err
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.path, "path", "", "Absolute path to the local git repo (required)")
	f.StringVar(&opts.id, "id", "", "Project id (default: derived by the daemon from the path)")
	f.StringVar(&opts.name, "name", "", "Display name")
	return cmd
}
