import { defineCloudflareConfig } from "@opennextjs/cloudflare";

// No incremental cache, no tag store, no queue: every route in this console is
// dynamic by nature - a thread, a calendar, a live screen - and there is
// nothing here worth revalidating on a schedule.
export default defineCloudflareConfig();
