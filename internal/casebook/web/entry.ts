// entry.ts — the esbuild entry point.
// Loads the app and boots it.

import { boot, registerSection, registerDock } from './app.ts';
import { makeAttention } from './attention.ts';
import { makeDock } from './dock.ts';

registerSection(makeAttention);
registerDock(makeDock);
boot();
