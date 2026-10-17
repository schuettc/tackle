// entry.ts — the esbuild entry point.
// Loads the app and boots it.

import { boot, registerSection, registerDock } from './app.ts';
import { makeApply } from './apply.ts';
import { makeDecide } from './decide-list.ts';
import { makeDock } from './dock.ts';

registerSection(makeDecide);
registerSection(makeApply);
registerDock(makeDock);
boot();
