// entry.ts — the esbuild entry point.
// Loads the app and boots it.

import { boot, registerSection } from './app.ts';
import { makeAttention } from './attention.ts';

registerSection(makeAttention);
boot();
