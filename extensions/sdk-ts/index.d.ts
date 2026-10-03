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

/**
 * One sprite for PiG's /sprite catalogue: the 16-by-14 pig the startup header
 * draws and /sprite preview shows beside the wordmark, colored by the palette. The host validates the grids, palette symbols, colors and text.
 */
export interface PiGSpriteDefinition {
  id: string;
  name: string;
  tagline: string;
  mascot: string[];
  palette: Record<string, string>;
}

declare module "@earendil-works/pi-coding-agent" {
  interface ExtensionUIContext {
    /** Set PiG's native login template, or reject an invalid definition. */
    setLogin(definition: PiGLoginDefinition): Promise<void>;
    /**
     * Add a sprite to PiG's /sprite catalogue, or reject an invalid definition.
     * Registering the same ID again replaces this extension's sprite; the sprite
     * leaves /sprite when the extension unloads.
     */
    registerSprite(definition: PiGSpriteDefinition): Promise<void>;
  }
}
