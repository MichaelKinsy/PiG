// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT
export default function (pi) {
  pi.registerCommand("editor-probe", {
    handler: async (_args, ctx) => {
      await ctx.ui.editor("External editor probe", "initial line\nsecond line");
    },
  });
}
