import { type BadgeRootProps, Root } from "@kobalte/core/badge";
import type { PolymorphicProps } from "@kobalte/core/polymorphic";
import type { VariantProps } from "class-variance-authority";
import { splitProps, type ValidComponent } from "solid-js";
import type { badgeVariants } from "~/components/ui/badge";
import { cn } from "~/lib/utils";

/**
 * App-specific tone variants layered on top of the zaidan registry badge.
 * The registry `components/ui/badge.tsx` is generated and must not be
 * hand-edited; tone variants live here instead.
 */
const toneVariants = {
	muted: "z-badge-variant-secondary",
	success: "z-badge-variant-success-light",
	warning: "z-badge-variant-warning-light",
	info: "z-badge-variant-info-light",
} as const;

type ToneVariant = keyof typeof toneVariants;
type PrimitiveVariant = NonNullable<
	VariantProps<typeof badgeVariants>["variant"]
>;

type BadgePrimitiveProps<T extends ValidComponent = "span"> = PolymorphicProps<
	T,
	BadgeRootProps<T>
> &
	VariantProps<typeof badgeVariants>;

export type BadgeVariant = PrimitiveVariant | ToneVariant;

export function Badge<T extends ValidComponent = "span">(
	props: Omit<BadgePrimitiveProps<T>, "variant"> & { variant?: BadgeVariant },
) {
	const [local, others] = splitProps(props as BadgePrimitiveProps, [
		"class",
		"variant",
	]);
	const toneClass =
		local.variant != null && local.variant in toneVariants
			? toneVariants[local.variant as ToneVariant]
			: undefined;
	return (
		<Root
			class={cn(toneClass, local.class)}
			data-variant={local.variant ?? "default"}
			{...others}
		/>
	);
}
