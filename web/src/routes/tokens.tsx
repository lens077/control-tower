import { createFileRoute } from "@tanstack/react-router";
import { TokensPage } from "@/components/TokensPage";

export const Route = createFileRoute("/tokens")({
  component: TokensPage,
});
