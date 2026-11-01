// Ported from design/v5/src/components/ui/button.tsx on 2026-10-31.
"use client";

import { Button as ButtonPrimitive } from "@base-ui/react/button";
import type { VariantProps } from "class-variance-authority";
import { LoaderCircle } from "lucide-react";

import { cn } from "../../lib/utils";

import { buttonVariants } from "./button-variants";

export type ButtonProps = ButtonPrimitive.Props &
  VariantProps<typeof buttonVariants> & {
    /** Shows a spinner, sets aria-busy and blocks clicks while keeping focus on the button. */
    loading?: boolean;
  };

/** The button for every action. Variants: primary | secondary | ghost | danger | link. Sizes: sm | md | lg | icon | icon-sm. */
function Button({ className, variant, size, loading = false, disabled, children, ...props }: ButtonProps) {
  return (
    <ButtonPrimitive
      data-slot="button"
      className={cn(buttonVariants({ variant, size }), className)}
      disabled={disabled || loading}
      focusableWhenDisabled={loading}
      aria-busy={loading || undefined}
      {...props}
    >
      {loading ? <LoaderCircle className="animate-spin motion-reduce:animate-none" aria-hidden /> : null}
      {children}
    </ButtonPrimitive>
  );
}

export { Button };
