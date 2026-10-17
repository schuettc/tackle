---
name: imprint
description: Guides npm package work through the imprint channel tools, from login through publishing a tarball, setting or moving a package's trusted publisher, renaming a package, deprecating one, and confirming a CI release. Use when the user asks to publish an npm package, set or move its trusted publisher, rename or deprecate a package, or check what landed on npm.
---

# npm work with imprint

You decide every step. imprint does the one npm step you ask for and tells you what it means: what happened, what npm is still doing, and what not to do yet. The user logs in to npm and approves each operation in the browser; imprint opens each approval link for them.

## How the tools behave

- `imprint_show` answers at once from the registry: versions, dist-tags, deprecation, and which repo's CI signed each version. Its trusted publisher follows as a message, since reading it may need the user's approval.
- Every other tool returns at once with what it started. Its progress arrives as messages from imprint: each approval link the user must open, `approved`, `live on npm`, or `failed:` with what npm's error usually means and the file holding npm's full output. Pass each approval link on to the user at once: an approval lasts 5 minutes, and imprint says so again with 1 minute left.
- A step is done only when imprint says the registry shows it. npm's own progress lines are not success.
- After a publish, npm takes 3 to 6 minutes to show the version. Don't work out the next version or publish again until imprint says it's live. A package's first publish shows as still processing in `imprint_show` until its version appears. npm keeps a `0.0.0-stage` entry in the version list for good after that, so beside real versions it means nothing.
- `imprint_status` lists what waits on the user's approval or on the registry.
- `failed:` that names `imprint_login` means npm isn't logged in: run `imprint_login`, then the step again. An expired approval means the user didn't approve in time: run the step again.

## Every trusted publisher gets one proven release

A new or moved trusted publisher stays unproven while no CI release signed by its repo is on npm. After `imprint_trust`, ask for that release and confirm it with `imprint_expect <package> <version> <repo>`. How to trigger the release (a version bump and tag, a workflow run) is the repo's own process; imprint only confirms it. Don't leave a trusted publisher unproven.

## Moving a trusted publisher

1. `imprint_show <package>` to see the current trusted publisher and who signed the latest version.
2. `imprint_trust <package> <repo> <workflow>`. It revokes the old trusted publisher and creates the new one; npm allows one per package.
3. One CI release from the new repo, confirmed with `imprint_expect`.

## Renaming a package

A trusted publisher can only be set on a package that exists, so the new name's first version is published by hand.

1. Build the tarball with the repo's own build, under the new name.
2. `imprint_publish <tarball>`. For a scoped name's first publish, imprint checks that the user belongs to the npm org. Wait until imprint says it's live.
3. `imprint_trust <new name> <repo> <workflow>`.
4. One CI release, confirmed with `imprint_expect`.
5. `imprint_deprecate <old name> "<message saying where it moved>"`.

## Publishing a version by hand

Prefer the repo's CI release when the package has a trusted publisher. When a hand publish is needed, give `imprint_publish` the tarball the repo's build made. imprint refuses a version already on npm or still processing.
