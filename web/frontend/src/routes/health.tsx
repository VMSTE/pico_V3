import { createFileRoute } from "@tanstack/react-router"

import { HealthPage } from "@/components/health/health-page"

export const Route = createFileRoute("/health")({
  component: HealthPage,
})
