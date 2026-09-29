import type { Meta, StoryObj } from "@storybook/react-vite";
import {
  useArgs,
  useEffect,
  useGlobals,
  useMemo,
  useRef,
} from "storybook/preview-api";
import { ColorSchemeCanvas } from "./color-scheme-canvas";
import { ColorSchemePage } from "./color-scheme-page";
import {
  colorSchemeArgTypes,
  colorSchemeOverrideStyle,
  darkColorSchemeDefaults,
  mergeColorSchemeArgs,
  readColorSchemeBaseline,
  type ColorSchemeControlArgs,
  type StorybookTheme,
} from "../tokens/color-scheme-args";

const meta = {
  title: "Design System/Color scheme",
  component: ColorSchemePage,
  parameters: { layout: "fullscreen" },
  argTypes: colorSchemeArgTypes,
  args: { ...darkColorSchemeDefaults },
} satisfies Meta<typeof ColorSchemePage> & {
  argTypes: typeof colorSchemeArgTypes;
  args: ColorSchemeControlArgs;
};

export default meta;
type Story = StoryObj<typeof meta>;

function useStoryTheme(): StorybookTheme {
  const [{ theme: globalTheme = "dark" }] = useGlobals();
  return globalTheme === "light" ? "light" : "dark";
}

export const Playground: Story = {
  render: function PlaygroundStory() {
    const storyTheme = useStoryTheme();
    const [args, updateArgs] = useArgs<ColorSchemeControlArgs>();
    const argsRef = useRef(args as ColorSchemeControlArgs);
    argsRef.current = args as ColorSchemeControlArgs;

    const baseline = useMemo(
      () => readColorSchemeBaseline(storyTheme),
      [storyTheme]
    );
    const baselineRef = useRef(baseline);
    const themeRef = useRef(storyTheme);
    const didInit = useRef(false);

    useEffect(() => {
      if (!didInit.current) {
        updateArgs(baseline);
        baselineRef.current = baseline;
        themeRef.current = storyTheme;
        didInit.current = true;
        return;
      }

      if (themeRef.current === storyTheme) return;

      const merged = mergeColorSchemeArgs(
        argsRef.current,
        baselineRef.current,
        baseline
      );
      updateArgs(merged);
      baselineRef.current = baseline;
      themeRef.current = storyTheme;
    }, [storyTheme, baseline, updateArgs]);

    const overrideStyle = colorSchemeOverrideStyle(
      args as ColorSchemeControlArgs,
      baseline
    );

    return (
      <ColorSchemeCanvas theme={storyTheme} overrideStyle={overrideStyle}>
        <ColorSchemePage showControlsHint />
      </ColorSchemeCanvas>
    );
  },
};

export const Reference: Story = {
  parameters: { controls: { disable: true } },
  render: function ReferenceStory() {
    const storyTheme = useStoryTheme();
    return (
      <ColorSchemeCanvas theme={storyTheme}>
        <ColorSchemePage />
      </ColorSchemeCanvas>
    );
  },
};
