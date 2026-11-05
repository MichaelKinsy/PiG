### Fixed

- Images in steering and follow-up messages queued while the agent is busy (`steer`, `followUp`, a prompt sent while streaming, RPC `steer` and `follow_up`) are now resized when the message is delivered, like images in idle prompts, instead of being sent and saved at full size; an image-only queued message now leaves the pending queue display after delivery (Pi #11738, #8581, D249, D250). <!-- fix: 7056d2d9e1 -->
