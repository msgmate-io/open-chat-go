"use client"

import { useRouter } from 'next/navigation'
import { Button } from "@/components/Button"
import { EyeIcon, EyeOff } from 'lucide-react';
import { ReloadIcon } from "@radix-ui/react-icons";
import { zodResolver } from "@hookform/resolvers/zod";
import { Toaster } from "@/components/Toaster"
import { FormProvider, useForm } from "react-hook-form";
import { useState, useEffect } from "react"
import { Input } from "@/components/Input";
import { z } from "zod";
import {
    FormControl,
    FormField,
    FormItem,
    FormMessage
} from "@/components/Form";


const formSchema = z.object({
    email: z.string().min(2, {
        message: "Username must be at least 2 characters.",
    }),
    password: z.string().min(8, {
        message: "Password must be at least 8 characters.",
    }),
})

type ErrorResult = null | { [key: string]: string }

export default function LoginHero() {
    const router = useRouter()
    const [showPassword, setShowPassword] = useState(false)
    const togglePasswordVisibility = () => setShowPassword(!showPassword);
    const [error, setError] = useState<ErrorResult>(null)
    
    const onSubmit = () => {
       console.log("submitted") 
       fetch("/api/v1/user/login", {
            method: "POST",
            body: JSON.stringify({
                ...form.getValues()
            })
       }).then(res => {
        if(res.ok){
            console.log("logged in!")
            router.push("/chat")
        }else{
            res.text().then(
                text => {
                    setError({non_field_errors: text})
                }
            ).catch(() => {
                setError({non_field_errors: "Error occurred while logging in."})
            })
        }
    }).catch(e => {
        setError(e.error)
       })
    }

    const form = useForm<z.infer<typeof formSchema>>({
        resolver: zodResolver(formSchema),
        defaultValues: {
            email: "",
            password: ""
        },
    })

    useEffect(() => {
        const onKeyDown = (event: KeyboardEvent) => {
            if (event.key === "Enter") {
                form.handleSubmit(onSubmit)();
            }
        };
        window.addEventListener("keydown", onKeyDown);
        return () => {
            window.removeEventListener("keydown", onKeyDown);
        };
    }, []);


    useEffect(() => {
        if (error) {
            Object.keys(error).forEach((key) => {
                form.setError(key, {
                    type: "server",
                    message: error[key],
                })
            });
        }
    }, [error])

    const {
        formState: { errors }
    } = form;
    console.log(error, errors)

    return (
        <div className="flex flex-col relative w-full gap-0">
            <FormProvider {...form}>
                <FormField
                    control={form.control}
                    name="email"
                    render={({ field }) => (
                        <FormItem
                            className="w-full space-y-0"
                        >
                            <FormControl className="">
                                <Input type="email" placeholder="Email" {...field} className="rounded-full py-6 text-lg text-center border-2" />
                            </FormControl>
                            <FormMessage className="text-center text-sm" />
                            {!errors?.email && <div className="text-red-500 text-sm text-center">&#8203;</div>}
                        </FormItem>
                    )} />
                <FormField
                    control={form.control}
                    name="password"
                    render={({ field }) => (
                        <FormItem
                            className="w-full space-y-0"
                        >
                            <FormControl>
                                <div className="relative">
                                    <Input type={showPassword ? 'text' : 'password'} placeholder="Password" {...field} className="rounded-full py-6 text-lg text-center border-2" />
                                    <div className="absolute inset-y-0 right-0 pr-3 flex items-center text-gray-400 cursor-pointer">
                                        {showPassword ? (
                                            <EyeOff className="h-6 w-6" onClick={togglePasswordVisibility} />
                                        ) : (
                                            <EyeIcon className="h-6 w-6" onClick={togglePasswordVisibility} />
                                        )}
                                    </div>
                                </div>
                            </FormControl>
                            <FormMessage className="text-center text-sm" />
                            {!errors?.password && <div className="text-red-500 text-sm text-center">&#8203;</div>}
                        </FormItem>
                    )}
                />
                <Button variant="outline" type="submit" className="rounded-full border-2" form="login-form" onClick={form.handleSubmit(onSubmit)} disabled={form.formState.isLoading}>
                    {form.formState.isLoading && <ReloadIcon className="mr-2 h-4 w-4 animate-spin" />}
                    Login
                </Button>
                {errors?.non_field_errors && <span className="text-red-500 text-sm text-center">{error?.non_field_errors}</span>}
                {!errors?.non_field_errors && <div className="text-red-500 text-sm text-center">&#8203;</div>}
            </FormProvider>
        </div>
    );
}


export function LoginSection({
    sectionId = "login",
    servicesList = [],
}) {

    return <div className="container py-24 sm:py-32 flex flex-col flex-grow items-center content-center justify-center" id={sectionId}>
        <div className="flex flex-col items-center content-center justify-center pb-2">
            <h1 className="text-2xl font-bold text-center">Welcome back!</h1>
            <p className="text-lg text-center">To Open-Chat! Login:</p>
        </div>
        or
        <LoginHero />
        <Toaster />
    </div>
}
