import { createFileRoute } from "@tanstack/react-router"

import { SecurityPage } from "@/components/security/security-page"

export const Route = createFileRoute("/security")({
  component: SecurityPage,
})
