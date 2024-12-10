import { useState } from "react";
import { cn } from "./utils";

export function CinematicLogo({
  className = "",
  onClick = () => { },
  clickedClass = "",
  size = 240,
  defaultClass = "",
  logoUrl = "https://msgmate.io/assets/logo.svg",
}) {
  <div className="absolute inset-0 "
      style={{
          backgroundImage: `url(${logoUrl})`,
          backgroundSize: 'cover',
          filter: 'grayscale(100%)',
      }}
  >
  </div>
  const [logoClicked, setLogoClicked] = useState(false);

  const handleLogoClick = () => {
      setLogoClicked(!logoClicked);
      onClick && onClick();
  };

  return (
      <div
          className={cn(
              `w-[${size}px] h-[${size}px] relative`,
              className,
              logoClicked ? clickedClass : defaultClass
          )}
          style={{
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
                  opacity: '0.9'
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
