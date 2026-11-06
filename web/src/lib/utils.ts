import { createCn } from "cn/config";

/**
 * Class merging (clsx + tailwind-merge's rules), told the console's own type
 * scale (globals.css --text-*): left alone it reads text-meta as a colour, and
 * cn("text-meta text-faint") dropped the size.
 */
export const cn = createCn({
  extend: {
    classGroups: {
      "font-size": [{ text: ["tiny", "meta", "aside", "ui", "body", "read", "title", "field", "heading", "lead", "display", "display-lg"] }],
    },
  },
});
