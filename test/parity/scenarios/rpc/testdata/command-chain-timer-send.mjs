export default function (pi) {
  pi.on("model_select", async () => {
    setTimeout(() => pi.sendMessage({ customType: "probe", content: "from-timer", display: true }), 0);
  });
}
