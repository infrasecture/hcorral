# Transition from myCodex

Hcorral deliberately has no compatibility or automatic migration layer. It
does not adopt myCodex containers, volumes, environment variables, Compose
files, images, or command names.

When a running or stopped myCodex container is verified for the same physical
workspace, hcorral exits with status 3 and performs no mutation. Use myCodex to
attach or run `myCodex down` in the original workspace first. Existing state
can be selected only by explicitly naming its Docker volume through the normal
`--state-volume` interface; hcorral never discovers or relabels it.
