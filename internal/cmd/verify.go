package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"acline/internal/store"
)

func newVerifyCmd(c *cli) *cobra.Command {
	var (
		verifyHead       bool
		verifyExpectHead string
		verifySealHead   bool
		verifyCheckSeal  bool
		verifyReseal     bool
	)
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Verify the audit trail's hash chain is intact",
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := c.st.VerifyChain()
			if err != nil {
				return err
			}
			if !r.OK() {
				return fmt.Errorf("audit trail FAILED verification at event #%d: %s", r.BadID, r.Reason)
			}
			rec, err := c.st.VerifyRecords()
			if err != nil {
				return err
			}
			if !rec.OK() {
				return fmt.Errorf("approvals/checks FAILED verification at %s: %s", rec.BadRef, rec.Reason)
			}
			if verifyExpectHead != "" {
				anchor, err := store.ParseAnchor(verifyExpectHead)
				if err != nil {
					return err
				}
				if err := c.st.CheckAnchor(anchor); err != nil {
					return fmt.Errorf("anchor check FAILED: %w", err)
				}
				fmt.Printf("anchor %d matches: nothing up to event #%d was truncated or rewritten\n", anchor.ID, anchor.ID)
			}
			fmt.Printf("audit trail intact: %d event(s) verified\n", r.Checked)
			fmt.Printf("approvals/checks intact: %d record(s) match their seals\n", rec.Checked)
			if r.Unhashed > 0 {
				fmt.Printf("note: %d event(s) predate hashing and were skipped\n", r.Unhashed)
			}
			if rec.Unsealed > 0 {
				fmt.Printf("note: %d approval/check record(s) predate sealing and cannot be verified\n", rec.Unsealed)
			}
			if r.Legacy() && !verifyReseal {
				fmt.Printf("note: %d event(s) from an older acline are tolerated, not checked; run `acline verify --reseal` to attest them and make verify strict\n", r.PreMarker)
			}
			if verifyReseal {
				err := c.withApprovalToken("reseal the store's legacy records", func(token string) error {
					res, err := c.st.Reseal(token)
					if err != nil {
						return err
					}
					if res.AlreadyOK {
						fmt.Println("already resealed: verify is strict for this store")
						return nil
					}
					fmt.Printf("resealed: %d legacy approval/check record(s) sealed, %d older event(s) attested (%d with no hash); verify is strict from now on\n", res.Sealed, res.Attested, res.Unhashed)
					return nil
				})
				if err != nil {
					return err
				}
			}
			if verifyCheckSeal {
				err := c.withApprovalToken("check the audit trail's head seal", func(token string) error {
					res, err := c.st.CheckHeadSeal(token)
					if err != nil {
						return err
					}
					if !res.Sealed {
						fmt.Println("head seal: none made with this token yet; create one with: acline verify --seal-head")
					} else {
						fmt.Printf("head seal #%d matches: nothing up to event #%d was rewritten\n", res.SealID, res.Head.ID)
					}
					if res.Foreign > 0 {
						fmt.Printf("note: %d newer seal(s) were made with another token (rotated since, or not genuine) and were not trusted\n", res.Foreign)
					}
					return nil
				})
				if err != nil {
					return err
				}
			}
			if verifySealHead {
				err := c.withApprovalToken("seal the audit trail's head", func(token string) error {
					head, err := c.st.SealHead(token)
					if err != nil {
						return err
					}
					fmt.Printf("sealed head %s with the approval token; check it later with: acline verify --check-seal\n", head)
					return nil
				})
				if err != nil {
					return err
				}
			}
			if verifyHead {
				if head, ok, err := c.st.ChainHead(); err != nil {
					return err
				} else if ok {
					fmt.Printf("head: %s\n", head)
					fmt.Println("record this somewhere the database's writers can't reach; later run: acline verify --expect-head <that value>")
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&verifyHead, "head", false, "also print the chain's head (<event-id>:<hash>) to record as an external anchor")
	cmd.Flags().BoolVar(&verifySealHead, "seal-head", false, "after verifying, seal the chain head with the approval token (needs the token), so a later rewrite of the trail can't be recomputed into a passing one")
	cmd.Flags().BoolVar(&verifyReseal, "reseal", false, "attest this store's legacy records (events from before hashing, approvals/checks from before sealing) so verify stops tolerating them: a person's call, needs the token when one is enabled")
	cmd.Flags().BoolVar(&verifyCheckSeal, "check-seal", false, "also check the newest head seal made with the approval token (needs the token)")
	cmd.Flags().StringVar(&verifyExpectHead, "expect-head", "", "fail unless the store still contains this anchored event (detects truncation of the newest events)")
	return cmd
}
