"use client"

import { Button, Text, TextTypes } from "@open-chat-go/ui"

export default function SignupRequestSentSection({
  navigateTo,
  email,
}: {
  navigateTo: (to: string) => void
  email?: string
}) {
  return (
    <div className="container py-24 sm:py-32 flex flex-col flex-grow items-center content-center justify-center text-foreground max-w-xl">
      <div className="flex flex-col items-center content-center justify-center pb-6 gap-2">
        <Text type={TextTypes.Heading5} tag="h1" bold center>
          Signup request sent
        </Text>
        <Text type={TextTypes.Body4} center>
          {email ? `Requested with: ${email}` : "Your signup request has been submitted."}
        </Text>
        <Text type={TextTypes.Body5} center color="muted">
          You will receive an email if the account is enabled.
        </Text>
      </div>
      <div className="flex flex-col relative w-full gap-3">
        <Button variant="ghost" className="rounded-full" onClick={() => navigateTo("/login")}>
          Back to login
        </Button>
      </div>
    </div>
  )
}
