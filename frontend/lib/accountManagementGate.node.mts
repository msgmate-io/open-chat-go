// Unit tests for the account creation gate (ci#1150).
//
// This file is `.mts` on purpose: the frontend `tsconfig.json` only includes
// `**/*.ts`/`**/*.tsx`, so a DOM-less `node --test` suite is not part of the
// build typecheck, and node >= 22 runs it directly via type stripping:
//
//   node --test frontend/lib/accountManagementGate.node.mts
//
// The gate lives in `accountManagementGate.ts` (no React import) so it is
// testable without a jsdom/Testing-Library harness, which this package does not
// have. See the PR description for what that leaves uncovered.
import test from "node:test"
import assert from "node:assert/strict"

import {
  SIGNUP_UNAVAILABLE_INTEGRATION_ABSENT_MESSAGE,
  landingTabForPathname,
  normalizePathname,
  resolveAccountManagementGate,
  resolveRegisterTabPresentation,
} from "./accountManagementGate.ts"

const ABSENT = resolveAccountManagementGate(undefined, { status: 404 })

test("a 404/error means the account_management integration is absent", () => {
  assert.equal(ABSENT.status, "absent")
  assert.equal(ABSENT.integrationPresent, false)
  assert.equal(ABSENT.signupAvailable, false)
  assert.equal(resolveRegisterTabPresentation(ABSENT), "integration-absent")
  assert.equal(SIGNUP_UNAVAILABLE_INTEGRATION_ABSENT_MESSAGE, "Account creation is not available on this server.")
})

test("no config and no error is the loading state, not absent", () => {
  const loading = resolveAccountManagementGate(undefined, undefined)
  assert.equal(loading.status, "loading")
  assert.equal(loading.integrationPresent, false)
  assert.equal(loading.signupAvailable, false)
})

test("a 200 with signup_enabled=true offers sign-up", () => {
  const present = resolveAccountManagementGate({ signup_enabled: true }, undefined)
  assert.equal(present.status, "present")
  assert.equal(present.integrationPresent, true)
  assert.equal(present.signupAvailable, true)
  assert.equal(resolveRegisterTabPresentation(present), "signup")
})

test("a 200 with signup_enabled=false keeps the closed-beta copy", () => {
  const disabled = resolveAccountManagementGate({ signup_enabled: false }, undefined)
  assert.equal(disabled.status, "present")
  assert.equal(disabled.integrationPresent, true)
  assert.equal(disabled.signupAvailable, false)
  assert.equal(resolveRegisterTabPresentation(disabled), "signup-disabled")
})

test("a 200 without signup_enabled is treated as disabled", () => {
  const present = resolveAccountManagementGate({}, undefined)
  assert.equal(present.integrationPresent, true)
  assert.equal(present.signupAvailable, false)
  assert.equal(resolveRegisterTabPresentation(present), "signup-disabled")
})

test("/sign-up selects the register tab when served by the landing shell", () => {
  assert.equal(landingTabForPathname("/sign-up"), "register")
  assert.equal(landingTabForPathname("/sign-up/"), "register")
  assert.equal(landingTabForPathname("/sign-up?email=a@b.c#x"), "register")
  assert.equal(landingTabForPathname("https://ci.msgmate.io/sign-up"), "register")
  assert.equal(landingTabForPathname("/"), null)
  assert.equal(landingTabForPathname("/login"), null)
  assert.equal(landingTabForPathname("/sign-up/confirm"), null)
  assert.equal(landingTabForPathname(""), null)
  assert.equal(landingTabForPathname(null), null)
  assert.equal(normalizePathname("/sign-up//"), "/sign-up")
})