import type { Meta, StoryObj } from "@storybook/react-vite";
import { Button } from "./button";
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "./card";

const meta = {
  component: Card,
} satisfies Meta<typeof Card>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Profile: Story = {
  render: () => (
    <Card className="w-[360px]">
      <CardHeader>
        <CardTitle>Account</CardTitle>
        <CardDescription>Manage your profile and security settings.</CardDescription>
      </CardHeader>
      <CardContent>
        <p className="text-sm text-muted-foreground">demo@open-chat-go.test</p>
      </CardContent>
      <CardFooter className="justify-end gap-2">
        <Button variant="outline">Cancel</Button>
        <Button>Save</Button>
      </CardFooter>
    </Card>
  ),
};

export const Compact: Story = {
  render: () => (
    <Card className="w-[280px]">
      <CardHeader>
        <CardTitle className="text-base">New chat</CardTitle>
      </CardHeader>
      <CardContent>
        <p className="text-sm">Start a conversation with your team.</p>
      </CardContent>
    </Card>
  ),
};
