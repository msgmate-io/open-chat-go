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
import { Input } from "@/components/Input";
import { Shield, QrCode, Key, AlertTriangle, CheckCircle, Copy, Download } from "lucide-react";
import { useState } from "react";

interface TwoFactorSetupData {
    secret: string;
    qr_code_url: string;
}

interface RecoveryCodesData {
    codes: string[];
}

export default function Page() {
    const { data: user, mutate: mutateUser } = useSWR(`/api/v1/user/self`, fetcher)
    const [setupData, setSetupData] = useState<TwoFactorSetupData | null>(null)
    const [verificationCode, setVerificationCode] = useState("")
    const [recoveryCodes, setRecoveryCodes] = useState<string[]>([])
    const [isLoading, setIsLoading] = useState(false)
    const [error, setError] = useState("")
    const [success, setSuccess] = useState("")

    const handleSetup2FA = async () => {
        setIsLoading(true)
        setError("")
        try {
            const response = await fetch('/api/v1/user/2fa/setup', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' }
            })
            if (!response.ok) throw new Error('Failed to setup 2FA')
            const data = await response.json()
            setSetupData(data)
        } catch (err) {
            setError(err instanceof Error ? err.message : 'Failed to setup 2FA')
        } finally {
            setIsLoading(false)
        }
    }

    const handleConfirm2FA = async () => {
        if (!setupData || !verificationCode) return
        
        setIsLoading(true)
        setError("")
        try {
            const response = await fetch('/api/v1/user/2fa/confirm', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    secret: setupData.secret,
                    code: verificationCode
                })
            })
            if (!response.ok) throw new Error('Invalid verification code')
            const data = await response.json()
            setRecoveryCodes(data.codes)
            setSuccess('2FA enabled successfully!')
            mutateUser() // Refresh user data
        } catch (err) {
            setError(err instanceof Error ? err.message : 'Failed to confirm 2FA')
        } finally {
            setIsLoading(false)
        }
    }

    const handleDisable2FA = async () => {
        if (!confirm('Are you sure you want to disable 2FA? This will make your account less secure.')) return
        
        setIsLoading(true)
        setError("")
        try {
            const response = await fetch('/api/v1/user/2fa/disable', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' }
            })
            if (!response.ok) throw new Error('Failed to disable 2FA')
            setSuccess('2FA disabled successfully!')
            setSetupData(null)
            setRecoveryCodes([])
            mutateUser() // Refresh user data
        } catch (err) {
            setError(err instanceof Error ? err.message : 'Failed to disable 2FA')
        } finally {
            setIsLoading(false)
        }
    }

    const handleGenerateNewRecoveryCodes = async () => {
        setIsLoading(true)
        setError("")
        try {
            const response = await fetch('/api/v1/user/2fa/recovery-codes', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' }
            })
            if (!response.ok) throw new Error('Failed to generate recovery codes')
            const data = await response.json()
            setRecoveryCodes(data.codes)
            setSuccess('New recovery codes generated!')
        } catch (err) {
            setError(err instanceof Error ? err.message : 'Failed to generate recovery codes')
        } finally {
            setIsLoading(false)
        }
    }

    const copyToClipboard = async (text: string) => {
        try {
            await navigator.clipboard.writeText(text)
            setSuccess('Copied to clipboard!')
        } catch (e) {
            setError('Failed to copy to clipboard')
        }
    }

    const downloadRecoveryCodes = () => {
        const content = recoveryCodes.join('\n')
        const blob = new Blob([content], { type: 'text/plain' })
        const url = URL.createObjectURL(blob)
        const a = document.createElement('a')
        a.href = url
        a.download = '2fa-recovery-codes.txt'
        a.click()
        URL.revokeObjectURL(url)
    }

    if (user?.isLoading) {
        return (
            <PlatformLayout descriptor="2FA Settings" basePath="/profile" baseTitle="Profile">
                <div className="space-y-4">
                    <div className="animate-pulse">
                        <div className="h-8 bg-muted rounded w-1/4 mb-2"></div>
                        <div className="h-4 bg-muted rounded w-1/2"></div>
                    </div>
                </div>
            </PlatformLayout>
        )
    }

    return (
        <PlatformLayout descriptor="2FA Settings" basePath="/profile" baseTitle="Profile">
            <div className="space-y-6">
                <div>
                    <Text type={TextTypes.Heading5} tag="h1" bold>
                        Two-Factor Authentication
                    </Text>
                    <Text type={TextTypes.Body5} color="muted">
                        Secure your account with two-factor authentication
                    </Text>
                </div>

                {error && (
                    <div className="bg-red-50 border border-red-200 rounded-md p-4 flex items-center gap-2">
                        <AlertTriangle className="h-5 w-5 text-red-600" />
                        <Text type={TextTypes.Body5} tag="span" className="text-red-800">{error}</Text>
                    </div>
                )}

                {success && (
                    <div className="bg-success/10 border border-success/30 rounded-md p-4 flex items-center gap-2">
                        <CheckCircle className="h-5 w-5 text-green-600" />
                        <Text type={TextTypes.Body5} tag="span" className="text-green-800">{success}</Text>
                    </div>
                )}

                {/* Current Status */}
                <Card>
                    <CardHeader>
                        <CardTitle className="flex items-center gap-2">
                            <Shield className="h-5 w-5" />
                            Current Status
                        </CardTitle>
                    </CardHeader>
                    <CardContent>
                        <div className="flex items-center justify-between">
                            <div>
                                <Text type={TextTypes.Body5} tag="div" bold>
                                    Two-Factor Authentication
                                </Text>
                                <Text type={TextTypes.Body6} color="muted">
                                    {user?.two_factor_enabled ? 'Enabled' : 'Disabled'}
                                </Text>
                            </div>
                            <Badge className={user?.two_factor_enabled ? "bg-success text-success-foreground" : "bg-muted-foreground text-background"}>
                                {user?.two_factor_enabled ? 'Enabled' : 'Disabled'}
                            </Badge>
                        </div>
                    </CardContent>
                </Card>

                {/* Setup 2FA */}
                {!user?.two_factor_enabled && !setupData && (
                    <Card>
                        <CardHeader>
                            <CardTitle>Enable Two-Factor Authentication</CardTitle>
                            <CardDescription>
                                Add an extra layer of security to your account
                            </CardDescription>
                        </CardHeader>
                        <CardContent>
                            <Button onClick={handleSetup2FA} disabled={isLoading}>
                                {isLoading ? 'Setting up...' : 'Enable 2FA'}
                            </Button>
                        </CardContent>
                    </Card>
                )}

                {/* QR Code Setup */}
                {setupData && !user?.two_factor_enabled && (
                    <Card>
                        <CardHeader>
                            <CardTitle>Scan QR Code</CardTitle>
                            <CardDescription>
                                Use your authenticator app to scan this QR code
                            </CardDescription>
                        </CardHeader>
                        <CardContent className="space-y-4">
                            <div className="flex flex-col items-center space-y-4">
                                <div className="bg-white p-4 rounded-lg border">
                                    <img 
                                        src={`https://api.qrserver.com/v1/create-qr-code/?size=200x200&data=${encodeURIComponent(setupData.qr_code_url)}`}
                                        alt="2FA QR Code"
                                        className="w-48 h-48"
                                    />
                                </div>
                                <div className="text-center">
                                    <Text type={TextTypes.Body6} color="muted" className="mb-2">
                                        Or enter this code manually:
                                    </Text>
                                    <div className="flex items-center gap-2">
                                        <code className="bg-muted px-2 py-1 rounded text-sm font-mono">
                                            {setupData.secret}
                                        </code>
                                        <Button 
                                            variant="outline" 
                                            size="sm"
                                            onClick={() => copyToClipboard(setupData.secret)}
                                        >
                                            <Copy className="h-4 w-4" />
                                        </Button>
                                    </div>
                                </div>
                            </div>
                            
                            <div className="space-y-2">
                                <Text type={TextTypes.Body6} tag="label" bold>
                                    Enter verification code:
                                </Text>
                                <div className="flex gap-2">
                                    <Input
                                        value={verificationCode}
                                        onChange={(e) => setVerificationCode(e.target.value)}
                                        placeholder="000000"
                                        maxLength={6}
                                        className="w-32"
                                    />
                                    <Button 
                                        onClick={handleConfirm2FA} 
                                        disabled={isLoading || verificationCode.length !== 6}
                                    >
                                        {isLoading ? 'Verifying...' : 'Verify & Enable'}
                                    </Button>
                                </div>
                            </div>
                        </CardContent>
                    </Card>
                )}

                {/* Recovery Codes */}
                {recoveryCodes.length > 0 && (
                    <Card>
                        <CardHeader>
                            <CardTitle className="flex items-center gap-2">
                                <Key className="h-5 w-5" />
                                Recovery Codes
                            </CardTitle>
                            <CardDescription>
                                Save these codes in a safe place. You can use them to access your account if you lose your device.
                            </CardDescription>
                        </CardHeader>
                        <CardContent className="space-y-4">
                            <div className="bg-yellow-50 border border-yellow-200 rounded-md p-4">
                                <div className="flex items-center gap-2 mb-2">
                                    <AlertTriangle className="h-5 w-5 text-yellow-600" />
                                    <Text type={TextTypes.Body5} tag="span" bold className="text-yellow-800">
                                        Important
                                    </Text>
                                </div>
                                <Text type={TextTypes.Body6} className="text-yellow-700">
                                    Each recovery code can only be used once. Store them in a safe place.
                                </Text>
                            </div>
                            
                            <div className="grid grid-cols-2 gap-2">
                                {recoveryCodes.map((code, index) => (
                                    <Text
                                        key={index}
                                        type={TextTypes.Body6}
                                        tag="div"
                                        className="bg-muted px-3 py-2 rounded font-mono"
                                    >
                                        {code}
                                    </Text>
                                ))}
                            </div>
                            
                            <div className="flex gap-2">
                                <Button 
                                    variant="outline" 
                                    onClick={() => copyToClipboard(recoveryCodes.join('\n'))}
                                >
                                    <Copy className="h-4 w-4 mr-2" />
                                    Copy All
                                </Button>
                                <Button 
                                    variant="outline" 
                                    onClick={downloadRecoveryCodes}
                                >
                                    <Download className="h-4 w-4 mr-2" />
                                    Download
                                </Button>
                            </div>
                        </CardContent>
                    </Card>
                )}

                {/* Manage 2FA */}
                {user?.two_factor_enabled && (
                    <Card>
                        <CardHeader>
                            <CardTitle>Manage Two-Factor Authentication</CardTitle>
                            <CardDescription>
                                2FA is currently enabled for your account
                            </CardDescription>
                        </CardHeader>
                        <CardContent className="space-y-4">
                            <div className="flex gap-2">
                                <Button 
                                    variant="outline" 
                                    onClick={handleGenerateNewRecoveryCodes}
                                    disabled={isLoading}
                                >
                                    {isLoading ? 'Generating...' : 'Generate New Recovery Codes'}
                                </Button>
                                <Button 
                                    variant="destructive" 
                                    onClick={handleDisable2FA}
                                    disabled={isLoading}
                                >
                                    {isLoading ? 'Disabling...' : 'Disable 2FA'}
                                </Button>
                            </div>
                        </CardContent>
                    </Card>
                )}
            </div>
        </PlatformLayout>
    )
}
