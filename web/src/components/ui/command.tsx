"use client"

import * as React from "react"
import { Command as CommandPrimitive } from "cmdk"
import { SearchIcon } from "lucide-react"
import { cn } from "cn"

import { InputGroup, InputGroupAddon } from "@/components/ui/input-group"

/**
 * A list found by typing and gone through with the arrow keys; Enter takes
 * the lit row. cmdk leaves every key alone while a Korean syllable is still
 * being composed, so an Enter that only finishes 한글 picks nothing.
 */
function Command({
  className,
  ...props
}: React.ComponentProps<typeof CommandPrimitive>) {
  return (
    <CommandPrimitive
      data-slot="command"
      className={cn("group/command flex w-full flex-col text-foreground", className)}
      {...props}
    />
  )
}

/** The field it is found by: the console's search field, magnifier and all. */
function CommandInput({
  className,
  wrapperClassName,
  icon = true,
  children,
  ...props
}: React.ComponentProps<typeof CommandPrimitive.Input> & {
  wrapperClassName?: string
  /** The magnifier before the words; off where a button at the end searches. */
  icon?: boolean
  /** What sits inside the field's edge after the words (InputGroupAddon). */
  children?: React.ReactNode
}) {
  return (
    <InputGroup data-slot="command-input-wrapper" className={wrapperClassName}>
      {icon ? (
        <InputGroupAddon>
          <SearchIcon aria-hidden />
        </InputGroupAddon>
      ) : null}
      <CommandPrimitive.Input
        data-slot="input-group-control"
        className={cn(
          "h-full min-w-0 flex-1 bg-transparent pr-3 text-[16px] text-foreground outline-none placeholder:text-faint disabled:cursor-not-allowed disabled:opacity-50 mouse:text-[13px]",
          icon ? "pl-1.5" : "pl-3",
          className
        )}
        {...props}
      />
      {children}
    </InputGroup>
  )
}

/**
 * The rows. cmdk puts them in a sizer of its own inside the list, so what
 * goes between rows is said one level down: *:space-y-px, *:divide-y.
 */
function CommandList({
  className,
  ...props
}: React.ComponentProps<typeof CommandPrimitive.List>) {
  return (
    <CommandPrimitive.List
      data-slot="command-list"
      className={cn("scroll-py-1 outline-none", className)}
      {...props}
    />
  )
}

function CommandEmpty({
  className,
  ...props
}: React.ComponentProps<typeof CommandPrimitive.Empty>) {
  return (
    <CommandPrimitive.Empty
      data-slot="command-empty"
      className={cn("px-2 py-4 text-[13px] text-faint", className)}
      {...props}
    />
  )
}

function CommandGroup({
  className,
  ...props
}: React.ComponentProps<typeof CommandPrimitive.Group>) {
  return (
    <CommandPrimitive.Group
      data-slot="command-group"
      className={cn(
        "**:[[cmdk-group-heading]]:px-2 **:[[cmdk-group-heading]]:py-1.5 **:[[cmdk-group-heading]]:text-[11.5px] **:[[cmdk-group-heading]]:text-faint",
        className
      )}
      {...props}
    />
  )
}

function CommandSeparator({
  className,
  ...props
}: React.ComponentProps<typeof CommandPrimitive.Separator>) {
  return (
    <CommandPrimitive.Separator
      data-slot="command-separator"
      className={cn("h-px bg-edge-soft", className)}
      {...props}
    />
  )
}

/**
 * A row. The lit one shows only while the field has the focus - on a phone
 * nobody is choosing with keys, and a row lit for no reason reads as picked.
 */
function CommandItem({
  className,
  ...props
}: React.ComponentProps<typeof CommandPrimitive.Item>) {
  return (
    <CommandPrimitive.Item
      data-slot="command-item"
      className={cn(
        "group/command-item relative flex cursor-pointer items-center gap-3 rounded-lg px-2 text-left outline-hidden select-none hover:bg-glass group-focus-within/command:data-[selected=true]:bg-glass data-[disabled=true]:pointer-events-none data-[disabled=true]:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
        className
      )}
      {...props}
    />
  )
}

export {
  Command,
  CommandInput,
  CommandList,
  CommandEmpty,
  CommandGroup,
  CommandItem,
  CommandSeparator,
}
