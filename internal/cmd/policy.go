package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"acline/internal/store"
)

var policyCmd = &cobra.Command{
	Use:   "policy",
	Short: "Inspect and check the active session's policy",
	Long: `A session's policy records what it was permitted to touch. acline cannot
enforce anything by itself — it is the harness's job to call "acline policy check"
before letting an agent act, and to respect a non-zero exit as a denial.`,
}

var policyShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the active session's parsed policy",
	RunE: func(cmd *cobra.Command, args []string) error {
		sess, err := st.CurrentSession()
		if err != nil {
			return err
		}
		p := store.ParsePolicy(sess.Policy.String)
		if p.Label != "" {
			fmt.Printf("label:       %s\n", p.Label)
		}
		printList := func(name string, vals []string) {
			if len(vals) == 0 {
				fmt.Printf("%s: (none)\n", name)
				return
			}
			fmt.Printf("%s: %v\n", name, vals)
		}
		printList("allow-tools", p.AllowTools)
		printList("deny-tools ", p.DenyTools)
		printList("allow-paths", p.AllowPaths)
		printList("deny-paths ", p.DenyPaths)
		if len(p.AllowTools) == 0 && len(p.DenyTools) == 0 && len(p.AllowPaths) == 0 && len(p.DenyPaths) == 0 {
			fmt.Println("\nno restrictions recorded — this session is unrestricted by policy")
		}
		return nil
	},
}

var (
	policyCheckTool string
	policyCheckPath string
)

var policyCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Check one action against the active session's policy; denial logs a policy_violation event",
	Long: `Intended to be called by a harness (a pre-tool-use hook, a sandbox entry
point) before it lets an agent act. Exits 0 and prints ALLOW on success; exits
1 and prints DENY on denial, having already recorded a policy_violation event
so the denial is evidence even if the harness ignores the exit code.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if policyCheckTool == "" {
			return fmt.Errorf("--tool is required")
		}
		allowed, reason, err := st.CheckPolicy(policyCheckTool, policyCheckPath)
		if err != nil {
			return err
		}
		if allowed {
			fmt.Println("ALLOW")
			return nil
		}
		fmt.Printf("DENY: %s\n", reason)
		return fmt.Errorf("denied by policy")
	},
}

func init() {
	policyCheckCmd.Flags().StringVar(&policyCheckTool, "tool", "", "tool/action name to check, e.g. write_file, network, run_shell")
	policyCheckCmd.Flags().StringVar(&policyCheckPath, "path", "", "filesystem path the action targets, if any")

	policyCmd.AddCommand(policyShowCmd, policyCheckCmd)
	rootCmd.AddCommand(policyCmd)
}
