#!/usr/bin/env perl
use strict;
use warnings;

my $chat = "frontend/packages/ui/src/components/chat";
my @files = glob("$chat/*.tsx");

for my $file (@files) {
  open my $fh, "<", $file or die $!;
  my $content = do { local $/; <$fh> };
  close $fh;

  $content =~ s{
    import \{ cn \} from ["']\@/components/utils["'];
  }{
    import { cn } from "../../lib/utils";
  }gx;

  $content =~ s{
    import \{ cn \} from ["']\@/lib/utils["'];
  }{
    import { cn } from "../../lib/utils";
  }gx;

  $content =~ s{
    from ["']\@/components/chat/([^"']+)["']
  }{
    from "./$1"
  }gx;

  $content =~ s{
    from ["']\@/components/DropdownMenu["']
  }{
    from "../dropdown-menu"
  }gx;

  $content =~ s{
    from ["']\@/components/Input["']
  }{
    from "../input"
  }gx;

  $content =~ s{
    from ["']\@/components/Textarea["']
  }{
    from "../textarea"
  }gx;

  $content =~ s{
    from ["']\@/components/AccountCardButton["']
  }{
    from "./account-card-button"
  }gx;

  $content =~ s{
    from ["']\@/components/CollapseIndicator["']
  }{
    from "./collapse-indicator"
  }gx;

  $content =~ s{
    from ["']\@/components/LoadingSpinner["']
  }{
    from "./loading-spinner"
  }gx;

  $content =~ s{
    import \{([^}]*)\} from ["']\@open-chat-go/ui["'];
  }{
    my $imports = $1;
    my @parts;
    for my $part (split /\s*,\s*/, $imports) {
      $part =~ s/^\s+|\s+$//g;
      if ($part =~ /^NeutralButton as (\w+)$/) {
        push @parts, "Button as $1";
      } elsif ($part eq "NeutralButton") {
        push @parts, "Button";
      } else {
        push @parts, $part;
      }
    }
    my %seen;
    @parts = grep { !$seen{$_}++ } @parts;
    my $from = "../" . (
      $imports =~ /\b(Card|CardHeader|CardTitle|CardDescription|CardContent|CardFooter)\b/ ? "card" :
      $imports =~ /\bBadge\b/ ? "badge" :
      $imports =~ /\bCheckbox\b/ ? "checkbox" :
      $imports =~ /\bToggle\b/ ? "toggle" :
      "button"
    );
    "import { " . join(", ", @parts) . " } from \"$from\";"
  }gex;

  $content =~ s/variant="ghost"/variant="neutral"/g
    if $file =~ /SendMessageButton/;

  $content =~ s/import \{ NeutralButton as Button \} from ["'][^"']+["'];/import { Button } from "..\/button";/g;
  $content =~ s/import \{ Button \} from ["']\@open-chat-go\/ui["'];/import { Button } from "..\/button";/g;

  open $fh, ">", $file or die $!;
  print $fh $content;
  close $fh;
}

# Account card + collapse
for my $file ("$chat/account-card-button.tsx", "$chat/collapse-indicator.tsx") {
  next unless -f $file;
  open my $fh, "<", $file or die $!;
  my $content = do { local $/; <$fh> };
  close $fh;
  $content =~ s{from ["']\@/components/DropdownMenu["']}{from "../dropdown-menu"}g;
  $content =~ s{from ["']\@open-chat-go/ui["']}{from "../card"}g;
  $content =~ s{from ["']\@/components/utils["']}{from "../../lib/utils"}g;
  $content =~ s{import image from ["']\@/assets/logo.png["']}{}g;
  $content =~ s{src=\{image\}}{src={avatarSrc}}g if $file =~ /account-card/;
  open $fh, ">", $file or die $!;
  print $fh $content;
  close $fh;
}
