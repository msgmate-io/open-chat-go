import React from "react";
import PlatformLayout from "@/components/PlatformLayout";
import useSWR from "swr";
import { fetcher } from "@/lib/utils";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  Badge,
  Button,
  Text,
  TextTypes,
} from "@open-chat-go/ui";
import { User, Shield, Calendar, Key, Bot } from "lucide-react";

function PageHeader({
  title,
  description,
}: {
  title: string;
  description: string;
}) {
  return (
    <div>
      <Text type={TextTypes.Heading5} tag="h1" bold>
        {title}
      </Text>
      <Text type={TextTypes.Body5} color="muted">
        {description}
      </Text>
    </div>
  );
}

function FieldLabel({ children }: { children: React.ReactNode }) {
  return (
    <Text type={TextTypes.Body6} color="muted" tag="div">
      {children}
    </Text>
  );
}

function FieldValue({
  children,
  mono,
  className,
}: {
  children: React.ReactNode;
  mono?: boolean;
  className?: string;
}) {
  return (
    <Text
      type={TextTypes.Body6}
      tag="div"
      bold={!mono}
      className={mono ? `font-mono ${className ?? ""}` : className}
    >
      {children}
    </Text>
  );
}

export default function Page() {
    const { data: user, isLoading, error } = useSWR(`/api/v1/user/self`, fetcher)
    
    if (isLoading) {
        return (
            <PlatformLayout descriptor="Profile" basePath="/profile" baseTitle="Profile">
                <div className="space-y-4">
                    <div className="animate-pulse">
                        <div className="h-8 bg-muted rounded w-1/4 mb-2"></div>
                        <div className="h-4 bg-muted rounded w-1/2"></div>
                    </div>
                    <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                        <div className="animate-pulse">
                            <div className="h-32 bg-gray-200 rounded"></div>
                        </div>
                        <div className="animate-pulse">
                            <div className="h-32 bg-gray-200 rounded"></div>
                        </div>
                    </div>
                </div>
            </PlatformLayout>
        )
    }

    if (error) {
        return (
            <PlatformLayout descriptor="Profile" basePath="/profile" baseTitle="Profile">
                <div className="space-y-4">
                    <PageHeader title="Profile" description="Error loading profile data" />
                </div>
            </PlatformLayout>
        )
    }

    return (
        <PlatformLayout descriptor="Profile" basePath="/profile" baseTitle="Profile">
            <div className="space-y-4">
                <PageHeader
                    title="Profile"
                    description="View and manage your account information"
                />
                
                <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
                    <Card>
                        <CardHeader>
                            <CardTitle className="flex items-center gap-2">
                                <User className="h-5 w-5" />
                                Basic Information
                            </CardTitle>
                            <CardDescription>Your account details</CardDescription>
                        </CardHeader>
                        <CardContent>
                            <div className="space-y-4">
                                <div className="flex items-center justify-between">
                                    <FieldLabel>Name</FieldLabel>
                                    <FieldValue>{user?.name || "—"}</FieldValue>
                                </div>
                                <div className="flex items-center justify-between">
                                    <FieldLabel>Email</FieldLabel>
                                    <FieldValue>{user?.email || "—"}</FieldValue>
                                </div>
                                <div className="flex items-center justify-between">
                                    <FieldLabel>User ID</FieldLabel>
                                    <FieldValue mono>{user?.id || "—"}</FieldValue>
                                </div>
                                <div className="flex items-center justify-between">
                                    <FieldLabel>Account Type</FieldLabel>
                                    <div className="flex items-center gap-2">
                                        {user?.is_admin ? (
                                            <Badge className="bg-purple-600 text-white">Admin</Badge>
                                        ) : (
                                            <Badge variant="secondary">User</Badge>
                                        )}
                                    </div>
                                </div>
                            </div>
                        </CardContent>
                    </Card>

                    <Card>
                        <CardHeader>
                            <CardTitle className="flex items-center gap-2">
                                <Shield className="h-5 w-5" />
                                Security Settings
                            </CardTitle>
                            <CardDescription>Two-factor authentication and security</CardDescription>
                        </CardHeader>
                        <CardContent>
                            <div className="space-y-4">
                                <div className="flex items-center justify-between">
                                    <FieldLabel>Two-Factor Authentication</FieldLabel>
                                    <div className="flex items-center gap-2">
                                        {user?.two_factor_enabled ? (
                                            <Badge className="bg-success text-success-foreground">Enabled</Badge>
                                        ) : (
                                            <Badge variant="outline">Disabled</Badge>
                                        )}
                                    </div>
                                </div>
                                <div className="flex items-center justify-between">
                                    <FieldLabel>Contact Token</FieldLabel>
                                    <FieldValue mono className="text-xs break-all">
                                        {user?.contact_token || "—"}
                                    </FieldValue>
                                </div>
                                <div className="pt-2">
                                    <Button 
                                        variant="outline" 
                                        size="sm" 
                                        className="w-full"
                                        onClick={() => window.location.href = '/profile/2fa'}
                                    >
                                        Manage Security Settings
                                    </Button>
                                </div>
                            </div>
                        </CardContent>
                    </Card>

                    <Card className="lg:col-span-2">
                        <CardHeader>
                            <CardTitle className="flex items-center gap-2">
                                <Calendar className="h-5 w-5" />
                                Account Information
                            </CardTitle>
                            <CardDescription>Account creation and activity details</CardDescription>
                        </CardHeader>
                        <CardContent>
                            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                                <div className="space-y-3">
                                    <div className="flex items-center justify-between">
                                        <FieldLabel>Created</FieldLabel>
                                        <Text type={TextTypes.Body6} tag="div">
                                            {user?.created_at ? new Date(user.created_at).toLocaleDateString() : "—"}
                                        </Text>
                                    </div>
                                    <div className="flex items-center justify-between">
                                        <FieldLabel>Last Updated</FieldLabel>
                                        <Text type={TextTypes.Body6} tag="div">
                                            {user?.updated_at ? new Date(user.updated_at).toLocaleDateString() : "—"}
                                        </Text>
                                    </div>
                                </div>
                                <div className="space-y-3">
                                    <div className="flex items-center justify-between">
                                        <FieldLabel>Contact Token</FieldLabel>
                                        <div className="flex items-center gap-2">
                                            <Key className="h-4 w-4 text-muted-foreground" />
                                            <Text type={TextTypes.Body7} tag="span" className="font-mono">
                                                {user?.contact_token ? `${user.contact_token.substring(0, 8)}...` : "—"}
                                            </Text>
                                        </div>
                                    </div>
                                </div>
                            </div>
                        </CardContent>
                    </Card>

                    <Card className="lg:col-span-2">
                        <CardHeader>
                            <CardTitle>Model Catalog</CardTitle>
                            <CardDescription>Explore available AI models and defaults</CardDescription>
                        </CardHeader>
                        <CardContent>
                            <Button
                                variant="outline"
                                onClick={() => window.location.href = '/models'}
                            >
                                Open Models Overview
                            </Button>
                        </CardContent>
                    </Card>

                    <Card className="lg:col-span-2">
                        <CardHeader>
                            <CardTitle className="flex items-center gap-2">
                                <Bot className="h-5 w-5" />
                                Bots
                            </CardTitle>
                            <CardDescription>Manage your bot runtimes and inspect their configs</CardDescription>
                        </CardHeader>
                        <CardContent>
                            <Button variant="outline" onClick={() => window.location.href = '/profile/bots'}>
                                Open Bots
                            </Button>
                        </CardContent>
                    </Card>

                    <Card className="lg:col-span-2">
                        <CardHeader>
                            <CardTitle>API Access Tokens</CardTitle>
                            <CardDescription>Create and manage bearer tokens for API clients</CardDescription>
                        </CardHeader>
                        <CardContent>
                            <Button variant="outline" onClick={() => window.location.href = '/profile/access-tokens'}>
                                Open API Tokens
                            </Button>
                        </CardContent>
                    </Card>
                </div>
            </div>
        </PlatformLayout>
    )
}
