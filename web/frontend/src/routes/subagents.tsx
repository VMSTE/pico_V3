import { createFileRoute } from "@tanstack/react-router"

import { SubagentsPage } from "@/components/subagents/subagents-page"

export const Route = createFileRoute("/subagents")({
  component: SubagentsPage,
})
