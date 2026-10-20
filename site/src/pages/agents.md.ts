import setupInstructions from "../../public/setup.md?raw";

/** Serves the agent setup instructions at /agents.md too, the emerging standard path for agent-facing site files. */
export function GET() {
  return new Response(setupInstructions, { headers: { "Content-Type": "text/markdown; charset=utf-8" } });
}
