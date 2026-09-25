import "@earendil-works/pi-coding-agent";

export type * from "@earendil-works/pi-coding-agent";

/**
 * Data rendered by PiG's fixed native login template.
 *
 * The host validates grid dimensions, palette symbols, colors, and display
 * widths before replacing the current header.
 */
export interface PiGLoginDefinition {
  brand: string[];
  hero: string[];
  mascot: string[];
  palette: Record<string, string>;
  name: string;
  description: string;
  tagline: string;
}

declare module "@earendil-works/pi-coding-agent" {
  interface ExtensionUIContext {
    /** Set PiG's native login template, or reject an invalid definition. */
    setLogin(definition: PiGLoginDefinition): Promise<void>;
  }
}
