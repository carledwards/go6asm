; More relocation shapes: label+/-const, <lo/>hi of CODE labels,
; indirect JMP through a DATA pointer, and .word * (current PC).
.segment "CODE"
entry:
        LDA tbl+2
        LDX tbl-1+1
        LDY #>entry
        LDA #<entry
        JMP (vec)
here:
        .word *
        .word here-entry
        .word tbl

.segment "RODATA"
tbl:
        .byte $10, $20, $30, $40

.segment "DATA"
vec:
        .res 2, $00
