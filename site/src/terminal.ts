/** ANSI codes for building terminal examples rendered by Expressive Code's `ansi` language. */
export const ANSI_DIM = "\x1b[2m";
export const ANSI_RESET = "\x1b[0m";

/** Formats an example as terminal output: lines starting with "$ " get a colored prompt and a bold command. */
export const toTerminalText = (example: string) =>
  example
    .split("\n")
    .map((line) => (line.startsWith("$ ") ? `\x1b[38;5;209m$${ANSI_RESET} \x1b[1m${line.slice(2)}${ANSI_RESET}` : line))
    .join("\n");
