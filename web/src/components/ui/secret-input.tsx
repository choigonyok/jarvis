"use client";

import { useState } from "react";
import { Input } from "@/components/ui/input";
import { Toggle } from "@/components/ui/toggle";
import { cn } from "@/lib/utils";

/**
 * A password field, hidden as typed, with a 보기 toggle - a phone keyboard
 * makes a typo easy and this is the only chance to see it. The card's secret
 * field and the login's password are the same field (2026-11-06 review: the
 * login had no way to look).
 */
export function SecretInput({
  id,
  value,
  onChange,
  autoComplete = "new-password",
  className,
  ...rest
}: Omit<React.ComponentProps<typeof Input>, "value" | "onChange" | "type"> & {
  value: string;
  onChange: (value: string) => void;
}) {
  const [shown, setShown] = useState(false);
  return (
    <div className="relative">
      <Input
        id={id}
        type={shown ? "text" : "password"}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        autoComplete={autoComplete}
        autoCapitalize="none"
        autoCorrect="off"
        spellCheck={false}
        className={cn("pe-14 mouse:text-[13.5px]", className)}
        {...rest}
      />
      <Toggle
        pressed={shown}
        onPressedChange={setShown}
        aria-label={shown ? "비밀번호 가리기" : "비밀번호 보기"}
        size="sm"
        className="absolute inset-y-0 right-1 my-auto h-9 rounded-md font-normal text-[12px] text-faint hover:bg-transparent hover:text-foreground data-[state=on]:bg-transparent mouse:h-7"
      >
        {shown ? "가리기" : "보기"}
      </Toggle>
    </div>
  );
}
