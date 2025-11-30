"use client"

import { useState } from "react";
import { cn } from "./utils";
import image from "../assets/logo.png"

export function CinematicLogo({
  className = "",
  onClick = () => { },
  clickedClass = "",
  size = 240,
  defaultClass = "",
  logoUrl = image,
  // When fluid, the logo fills its (responsive) parent instead of forcing a
  // fixed pixel square, so callers can clamp it to the viewport.
  fluid = false,
}: {
  className?: string;
  onClick?: () => void;
  clickedClass?: string;
  size?: number;
  defaultClass?: string;
  logoUrl?: string;
  fluid?: boolean;
}) {
  const [logoClicked, setLogoClicked] = useState(false);

  const handleLogoClick = () => {
      setLogoClicked(!logoClicked);
      onClick && onClick();
  };

  return (
      <div
          className={cn(
              fluid ? "size-full" : `w-[${size}px] h-[${size}px]`,
              "relative",
              className,
              logoClicked ? clickedClass : defaultClass
          )}
          style={fluid ? undefined : {
              width: `${size}px`,
              height: `${size}px`
          }}
          onClick={handleLogoClick}
      >
          <div className="absolute inset-0 "
              style={{
                  backgroundImage: `url(${logoUrl})`,
                  backgroundSize: 'cover',
                  filter: 'grayscale(100%)',
              }}
          >
          </div>
           <div
               className="absolute inset-0 gradient-overlay"
               style={{
                   maskImage: `url(${logoUrl})`,
                   WebkitMaskImage: `url(${logoUrl})`,
                   maskSize: 'cover',
                   WebkitMaskSize: 'cover',
                    opacity: '0.55'
               }}
           ></div>
          <div className="absolute inset-0 z-30"
              style={{
                  backgroundImage: `url(${logoUrl})`,
                  backgroundSize: 'cover',
                  filter: 'grayscale(100%)',
                  opacity: '0.4'
              }}
          >
          </div>
      </div>
  );
}
