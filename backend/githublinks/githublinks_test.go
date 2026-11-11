package githublinks

import "testing"

func TestHumanURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "issue api url becomes web url",
			in:   "https://api.github.com/repos/a-little-world/little-world-backend/issues/920",
			want: "https://github.com/a-little-world/little-world-backend/issues/920",
		},
		{
			name: "pull api url becomes web url",
			in:   "https://api.github.com/repos/msgmate-io/open-chat-go/pulls/1027",
			want: "https://github.com/msgmate-io/open-chat-go/pulls/1027",
		},
		{
			name: "comment api url becomes web url",
			in:   "https://api.github.com/repos/msgmate-io/open-chat-go/issues/comments/123",
			want: "https://github.com/msgmate-io/open-chat-go/issues/comments/123",
		},
		{
			name: "human url is left untouched",
			in:   "https://github.com/a-little-world/little-world-backend/issues/920",
			want: "https://github.com/a-little-world/little-world-backend/issues/920",
		},
		{
			name: "surrounding whitespace is trimmed",
			in:   "  https://api.github.com/repos/acme/widgets/issues/7  ",
			want: "https://github.com/acme/widgets/issues/7",
		},
		{
			name: "trailing slash is dropped",
			in:   "https://api.github.com/repos/acme/widgets/issues/7/",
			want: "https://github.com/acme/widgets/issues/7",
		},
		{
			name: "host match is case insensitive",
			in:   "https://API.GitHub.com/repos/acme/widgets/issues/7",
			want: "https://github.com/acme/widgets/issues/7",
		},
		{
			name: "unrelated host is left untouched",
			in:   "https://example.com/repos/acme/widgets/issues/7",
			want: "https://example.com/repos/acme/widgets/issues/7",
		},
		{
			name: "api host without repos path is left untouched",
			in:   "https://api.github.com/user",
			want: "https://api.github.com/user",
		},
		{
			name: "repos path without repository is left untouched",
			in:   "https://api.github.com/repos/",
			want: "https://api.github.com/repos/",
		},
		{
			name: "empty stays empty",
			in:   "   ",
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HumanURL(tc.in); got != tc.want {
				t.Fatalf("HumanURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
