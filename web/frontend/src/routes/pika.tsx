import { createFileRoute } from "@tanstack/react-router"

import { PikaPage } from "@/components/pika/pika-page"

export const Route = createFileRoute("/pika")({
  component: PikaPage,
})
