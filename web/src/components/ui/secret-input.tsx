"use client";

import { useState } from "react";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

/**
 * A password field, hidden as typed, with a 보기 toggle - a phone keyboard
 * makes a typo easy and this is the only chance to see it. The card's secret
 * field and the login's password are the same field.
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
        className={cn("pe-14 mouse:text-ui", className)}
        {...rest}
      />
      {/* A button that says what it will do, not a toggle: pressed as well,
          it was read "비밀번호 가리기, 선택됨" - as if hiding were on while
          the password showed. */}
      <button
        type="button"
        onClick={() => setShown((s) => !s)}
        aria-label={shown ? "비밀번호 가리기" : "비밀번호 보기"}
        className="absolute inset-y-0 right-1 my-auto inline-flex h-9 min-w-7 items-center justify-center rounded-md px-2.5 text-aside font-normal whitespace-nowrap text-faint transition-colors hover:text-foreground focus-ring disabled:pointer-events-none disabled:opacity-40 mouse:h-7"
      >
        {shown ? "가리기" : "보기"}
      </button>
    </div>
  );
}
