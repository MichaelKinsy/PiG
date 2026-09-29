import { ScrollView, Text } from "@earendil-works/pi-tui";

export default function (pi) {
  let view;
  let render;
  let remainder = 0;
  pi.on("session_start", (_event, ctx) => {
    ctx.ui.setFooter((tui, _theme, footerData) => {
      render = () => tui.requestRender();
      view = new ScrollView(new Text("\x1b[36mSCROLL FOOTER 界\x1b[39m", 0, 0), { follow: "end", scrollbar: "always" });
      view.updateLayout(9, 3, render);
      // gentle-pi's sidebar installation binds this inherited method even in regular mode.
      view.handleMouse.bind(view);
      return {
        invalidate: () => view.invalidate(),
        render: width => [
          ...view.render(width),
          `top=${view.scrollTop} viewport=${view.viewportHeight} follow=${view.isFollowingEnd} remainder=${remainder} branch=${footerData.getGitBranch() ?? "none"}`,
        ],
      };
    });
  });
  pi.registerCommand("scroll-footer", {
    handler: (_args, ctx) => {
      remainder = view.scrollBy(-20);
      render();
      ctx.ui.notify("SCROLL FOOTER RESULT", "info");
    },
  });
}
