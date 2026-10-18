# notifykit context

**Thread.** One post in a channel, its copy in the channel's discussion group, and one comment that replies to that copy. The caller stores the three message ids (`thread.IDs`) and hands them back on the next `Publish`.

**Copy.** The message Telegram forwards on its own from a channel into the linked discussion group (`is_automatic_forward`). A comment can only be a reply to it. It arrives as an update a moment after the post, sometimes much later, sometimes never.

**Discussion.** The group linked to a channel, read from `getChat(channel).linked_chat_id`. A channel may have none.

**Outcome.** What `Publish` did to the comment: commented in the thread, waiting for the copy, gave up on the copy, posted without a thread, or found the post deleted.

**Unthreaded comment.** A comment sent to the discussion group without a reply because the copy never arrived or was deleted. The text reaches readers, the thread link does not.
