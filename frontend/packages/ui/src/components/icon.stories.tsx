import type { Meta, StoryObj } from "@storybook/react-vite"
import { Icon, iconRegistry, type IconName } from "./icon"

const meta = {
  component: Icon,
  title: "Components/Icon",
  argTypes: {
    name: {
      control: "select",
      options: Object.keys(iconRegistry) as IconName[],
    },
    size: {
      control: "select",
      options: ["xs", "sm", "md", "lg", "xl"],
    },
  },
} satisfies Meta<typeof Icon>

export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {
  args: {
    name: "pencil",
    size: "md",
  },
}

export const AllIcons: Story = {
  render: () => (
    <div className="flex flex-wrap items-center gap-4 text-foreground">
      {(Object.keys(iconRegistry) as IconName[]).map((name) => (
        <div
          key={name}
          className="flex w-24 flex-col items-center gap-2 rounded-md border border-border p-3"
        >
          <Icon name={name} size="lg" />
          <span className="text-xs text-muted-foreground">{name}</span>
        </div>
      ))}
    </div>
  ),
}
