import type { Meta, StoryObj } from "@storybook/react-vite";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "../dropdown-menu";
import { Icon } from "../icon";
import { AccountCardButton } from "./AccountCardButton";

const meta = {
  title: "Chat/AccountCardButton",
  component: AccountCardButton,
} satisfies Meta<typeof AccountCardButton>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
  render: () => (
    <DropdownMenu>
      <AccountCardButton
        displayName="Demo User"
        hint={<Icon name="chevron-down" size="sm" className="text-muted-foreground" />}
      />
      <DropdownMenuContent className="w-56">
        <DropdownMenuItem>Menu opens on click</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  ),
};

export const InitialFallback: Story = {
  render: () => (
    <DropdownMenu>
      <AccountCardButton displayName="Tim" />
      <DropdownMenuContent className="w-56">
        <DropdownMenuItem>No avatar image</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  ),
};
