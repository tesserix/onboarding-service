package main

import (
	"encoding/json"
	"fmt"

	"github.com/tesserix/onboarding-service/api/staffcontract"
)

func runtimeStaffSetup() string {
	states, _ := json.Marshal(staffcontract.ReceiptStates())
	return fmt.Sprintf(`
import { z } from "zod";

export const StaffInvitationReceiptContract = z.object({
  state: z.enum(%s),
  invitation_id: z.uuidv7().optional(),
  phase: z.enum(["email", "password"]).optional(),
  expires_at: z.iso.datetime({offset: true}).optional(),
}).strict().superRefine((value, ctx) => {
  if (value.invitation_id === undefined) {
    if (value.state !== "complete" || value.phase !== undefined || value.expires_at !== undefined) {
      ctx.addIssue({code: "custom", message: "Unbound setup receipt"});
    }
  } else if (value.phase === undefined || value.expires_at === undefined) {
    ctx.addIssue({code: "custom", message: "Incomplete setup receipt"});
  }
}) satisfies z.ZodType<StaffInvitationReceipt>;
`, states)
}
